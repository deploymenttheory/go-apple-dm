package axmtest

import (
	"encoding/csv"
	"net/http"
	"strings"
	"time"
	"uuid"
)

// Activity types and statuses.
const (
	ActivityAssign         = "ASSIGN_DEVICES"
	ActivityUnassign       = "UNASSIGN_DEVICES"
	ActivityAssignDeadline = "ASSIGN_DEVICES_WITH_MDM_MIGRATION_DEADLINE"
	ActivityUpdateDeadline = "UPDATE_MDM_MIGRATION_DEADLINE"
	ActivityCancel         = "CANCEL_MDM_MIGRATION"
	ActivityRelease        = "RELEASE_DEVICES"

	StatusInProgress = "IN_PROGRESS"
	StatusCompleted  = "COMPLETED"

	SubStatusSubmitted            = "SUBMITTED"
	SubStatusProcessing           = "PROCESSING"
	SubStatusCompletedWithSuccess = "COMPLETED_WITH_SUCCESS"
	SubStatusCompletedWithError   = "COMPLETED_WITH_ERROR"

	// MaxMigrationDeadline is how far ahead a migration deadline may be.
	MaxMigrationDeadline = 90 * 24 * time.Hour
)

// activity is one org device activity in the engine.
type activity struct {
	id        string
	typ       string
	serverID  string
	serials   []string
	deadline  time.Time
	status    string
	subStatus string
	created   time.Time
	completed time.Time
	step      int
	rows      [][]string
}

// terminal reports whether the activity finished.
func (a *activity) terminal() bool { return a.status != StatusInProgress }

// Activity returns an activity's status and sub-status.
func (s *Server) Activity(id string) (status, subStatus string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.acts[id]
	if !ok {
		return "", "", false
	}
	return a.status, a.subStatus, true
}

// Advance moves every unfinished activity one step: SUBMITTED to
// PROCESSING, then to COMPLETED with the assignments applied. It returns
// how many activities moved.
func (s *Server) Advance() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.advanceLocked()
}

// Complete advances until no activity is left unfinished.
func (s *Server) Complete() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.advanceLocked() > 0 {
	}
}

// AutoAdvance calls Advance every interval until Close or a zero interval.
func (s *Server) AutoAdvance(every time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ticker != nil {
		s.ticker.Stop()
		close(s.stop)
		s.ticker, s.stop = nil, nil
	}
	if every <= 0 {
		return
	}
	s.ticker = time.NewTicker(every)
	s.stop = make(chan struct{})
	go func(t *time.Ticker, stop chan struct{}) {
		for {
			select {
			case <-t.C:
				s.Advance()
			case <-stop:
				return
			}
		}
	}(s.ticker, s.stop)
}

func (s *Server) advanceLocked() int {
	moved := 0
	now := s.now().UTC()
	for _, id := range s.actOrder {
		a := s.acts[id]
		if a.terminal() {
			continue
		}
		moved++
		a.step++
		switch a.step {
		case 1:
			a.subStatus = SubStatusProcessing
		default:
			s.applyLocked(a, now)
		}
	}
	return moved
}

// applyLocked completes a and applies its effect.
func (s *Server) applyLocked(a *activity, now time.Time) {
	a.status, a.subStatus, a.completed = StatusCompleted, SubStatusCompletedWithSuccess, now
	visible := now.Add(s.lag)
	for _, serial := range a.serials {
		if reason, failed := s.outcomes[serial]; failed {
			a.rows = append(a.rows, []string{serial, a.typ, "FAILED", reason})
			a.subStatus = SubStatusCompletedWithError
			continue
		}
		dev, ok := s.store.devices.get(serial)
		if !ok {
			a.rows = append(a.rows, []string{serial, a.typ, "FAILED", "device is not in the organization"})
			a.subStatus = SubStatusCompletedWithError
			continue
		}
		if reason := s.applyOne(a, dev, visible, now); reason != "" {
			a.rows = append(a.rows, []string{serial, a.typ, "FAILED", reason})
			a.subStatus = SubStatusCompletedWithError
			continue
		}
		a.rows = append(a.rows, []string{serial, a.typ, "SUCCESS", ""})
	}
	s.refreshDeviceCounts(now)
}

// applyOne applies a to one device and returns a failure reason or "".
func (s *Server) applyOne(a *activity, dev *resource, visible, now time.Time) string {
	serverName := ""
	if srv, ok := s.store.servers.get(a.serverID); ok {
		serverName, _ = srv.attrs["serverName"].(string)
	}
	switch a.typ {
	case ActivityAssign, ActivityAssignDeadline:
		s.store.assignments[dev.id] = assignment{serverID: a.serverID, visibleAt: visible}
		if a.typ == ActivityAssignDeadline {
			dev.attrs["mdmMigrationStatus"] = "REQUESTED"
			dev.attrs["mdmMigrationDeadlineDateTime"] = a.deadline.UTC().Format(time.RFC3339)
		}
		s.addAuditLocked(uuid.New().String(), map[string]any{
			"eventDateTime": now, "type": "DEVICE_ASSIGNED_TO_SERVER", "category": "DEVICE_MANAGEMENT",
			"actorType": "API_USER", "actorId": "api", "subjectType": "DEVICE", "subjectId": dev.id,
			"eventDataPropertyKey":            "eventDataDeviceAssignedToServer",
			"eventDataDeviceAssignedToServer": map[string]any{"serialNumber": dev.id, "targetServerName": serverName},
		})
	case ActivityUnassign:
		s.store.assignments[dev.id] = assignment{serverID: "", visibleAt: visible}
		s.addAuditLocked(uuid.New().String(), map[string]any{
			"eventDateTime": now, "type": "DEVICE_UNASSIGNED_FROM_SERVER", "category": "DEVICE_MANAGEMENT",
			"actorType": "API_USER", "actorId": "api", "subjectType": "DEVICE", "subjectId": dev.id,
			"eventDataPropertyKey":                "eventDataDeviceUnassignedFromServer",
			"eventDataDeviceUnassignedFromServer": map[string]any{"serialNumber": dev.id},
		})
	case ActivityRelease:
		delete(s.store.assignments, dev.id)
		s.store.devices.del(dev.id)
		s.addAuditLocked(uuid.New().String(), map[string]any{
			"eventDateTime": now, "type": "DEVICE_REMOVED_FROM_ORG", "category": "DEVICE_INVENTORY",
			"actorType": "API_USER", "actorId": "api", "subjectType": "DEVICE", "subjectId": dev.id,
			"eventDataPropertyKey":          "eventDataDeviceRemovedFromOrg",
			"eventDataDeviceRemovedFromOrg": map[string]any{"serialNumber": dev.id, "releaseEntityType": "API"},
		})
	case ActivityUpdateDeadline:
		if _, migrating := dev.attrs["mdmMigrationStatus"]; !migrating {
			return "device is not undergoing a device management service migration"
		}
		dev.attrs["mdmMigrationDeadlineDateTime"] = a.deadline.UTC().Format(time.RFC3339)
	case ActivityCancel:
		if _, migrating := dev.attrs["mdmMigrationStatus"]; !migrating {
			return "device is not undergoing a device management service migration"
		}
		delete(dev.attrs, "mdmMigrationStatus")
		delete(dev.attrs, "mdmMigrationDeadlineDateTime")
	}
	dev.attrs["updatedDateTime"] = now
	return ""
}

// activityRequest is the wire form of Create an OrgDeviceActivity.
type activityRequest struct {
	Data struct {
		Type       string `json:"type"`
		Attributes struct {
			ActivityType         string `json:"activityType"`
			ActivityTypeMetadata *struct {
				MDMMigrationDeadlineDateTime time.Time `json:"mdmMigrationDeadlineDateTime"`
			} `json:"activityTypeMetadata"`
		} `json:"attributes"`
		Relationships struct {
			MDMServer *struct {
				Data struct {
					Type string `json:"type"`
					ID   string `json:"id"`
				} `json:"data"`
			} `json:"mdmServer"`
			Devices struct {
				Data []struct {
					Type string `json:"type"`
					ID   string `json:"id"`
				} `json:"data"`
			} `json:"devices"`
		} `json:"relationships"`
	} `json:"data"`
}

// handleCreateActivity is POST /v1/orgDeviceActivities.
func (s *Server) handleCreateActivity(w http.ResponseWriter, r *http.Request) {
	var req activityRequest
	if err := readJSON(r, &req); err != nil {
		s.apiError(w, http.StatusBadRequest, "PARAMETER_ERROR.INVALID", "malformed JSON body", map[string]any{"pointer": "/data"})
		return
	}
	if req.Data.Type != typeOrgDeviceActivities {
		s.apiError(w, http.StatusBadRequest, "ENTITY_ERROR.ATTRIBUTE.INVALID", "type must be orgDeviceActivities", map[string]any{"pointer": "/data/type"})
		return
	}
	typ := req.Data.Attributes.ActivityType
	needsServer, needsDeadline := false, false
	switch typ {
	case ActivityAssign:
		needsServer = true
	case ActivityAssignDeadline:
		needsServer, needsDeadline = true, true
	case ActivityUpdateDeadline:
		needsDeadline = true
	case ActivityUnassign, ActivityCancel, ActivityRelease:
	default:
		s.apiError(w, http.StatusBadRequest, "ENTITY_ERROR.ATTRIBUTE.INVALID", "unknown activityType", map[string]any{"pointer": "/data/attributes/activityType"})
		return
	}
	if len(req.Data.Relationships.Devices.Data) == 0 {
		s.apiError(w, http.StatusBadRequest, "ENTITY_ERROR.RELATIONSHIP.REQUIRED", "at least one device is required", map[string]any{"pointer": "/data/relationships/devices"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	a := &activity{id: uuid.New().String(), typ: typ, status: StatusInProgress, subStatus: SubStatusSubmitted, created: now}
	if needsServer {
		if req.Data.Relationships.MDMServer == nil {
			s.apiError(w, http.StatusBadRequest, "ENTITY_ERROR.RELATIONSHIP.REQUIRED", typ+" requires an mdmServer", map[string]any{"pointer": "/data/relationships/mdmServer"})
			return
		}
		a.serverID = req.Data.Relationships.MDMServer.Data.ID
		if _, ok := s.store.servers.get(a.serverID); !ok {
			s.conflict(w, "ENTITY_ERROR.RELATIONSHIP.INVALID", "invalid device management service id", "/data/relationships/mdmServer/data/id")
			return
		}
	}
	if needsDeadline {
		if req.Data.Attributes.ActivityTypeMetadata == nil || req.Data.Attributes.ActivityTypeMetadata.MDMMigrationDeadlineDateTime.IsZero() {
			s.apiError(w, http.StatusBadRequest, "ENTITY_ERROR.ATTRIBUTE.REQUIRED", typ+" requires activityTypeMetadata.mdmMigrationDeadlineDateTime", map[string]any{"pointer": "/data/attributes/activityTypeMetadata"})
			return
		}
		a.deadline = req.Data.Attributes.ActivityTypeMetadata.MDMMigrationDeadlineDateTime
		if a.deadline.After(now.Add(MaxMigrationDeadline)) {
			s.conflict(w, "ENTITY_ERROR.ATTRIBUTE.INVALID", "migration deadline is outside the allowed range", "/data/attributes/activityTypeMetadata/mdmMigrationDeadlineDateTime")
			return
		}
	}
	for _, d := range req.Data.Relationships.Devices.Data {
		if d.Type != typeOrgDevices {
			s.apiError(w, http.StatusBadRequest, "ENTITY_ERROR.RELATIONSHIP.INVALID", "device type must be orgDevices", map[string]any{"pointer": "/data/relationships/devices/data/type"})
			return
		}
		if _, ok := s.store.devices.get(d.ID); !ok {
			s.conflict(w, "ENTITY_ERROR.RELATIONSHIP.INVALID", "invalid device serial number "+d.ID, "/data/relationships/devices/data/id")
			return
		}
		a.serials = append(a.serials, d.ID)
	}
	s.acts[a.id] = a
	s.actOrder = append(s.actOrder, a.id)
	writeJSON(w, http.StatusCreated, map[string]any{"data": s.renderActivity(a, nil), "links": map[string]any{"self": s.URL + "/v1/orgDeviceActivities"}})
}

// renderActivity is the JSON:API form of an activity.
func (s *Server) renderActivity(a *activity, sel map[string]struct{}) map[string]any {
	attrs := map[string]any{"status": a.status, "subStatus": a.subStatus, "createdDateTime": a.created, "activityType": a.typ}
	if a.terminal() {
		attrs["completedDateTime"] = a.completed
		attrs["downloadUrl"] = s.URL + "/v1/orgDeviceActivities/" + a.id + "/download"
	}
	return s.render(&resource{typ: typeOrgDeviceActivities, id: a.id, attrs: normalize(attrs)}, sel)
}

// handleGetActivity is GET /v1/orgDeviceActivities/{id}.
func (s *Server) handleGetActivity(w http.ResponseWriter, r *http.Request) {
	sel, ok := s.selectFields(w, r, typeOrgDeviceActivities)
	if !ok {
		return
	}
	id := r.PathValue("id")
	s.mu.Lock()
	a, found := s.acts[id]
	var doc map[string]any
	if found {
		doc = s.renderActivity(a, sel)
	}
	s.mu.Unlock()
	if !found {
		s.notFound(w, typeOrgDeviceActivities, id)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": doc, "links": map[string]any{"self": s.URL + r.URL.RequestURI()}})
}

// handleActivityLog is GET /v1/orgDeviceActivities/{id}/download: the CSV
// with one row per serial.
func (s *Server) handleActivityLog(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var b strings.Builder
	s.mu.Lock()
	a, found := s.acts[id]
	ready := found && a.terminal()
	if ready {
		cw := csv.NewWriter(&b)
		_ = cw.Write([]string{"serialNumber", "activityType", "status", "reason"})
		_ = cw.WriteAll(a.rows)
	}
	s.mu.Unlock()
	if !ready {
		s.notFound(w, typeOrgDeviceActivities, id)
		return
	}
	w.Header().Set("Content-Type", "text/csv")
	_, _ = w.Write([]byte(b.String()))
}
