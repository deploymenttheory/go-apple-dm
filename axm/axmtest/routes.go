package axmtest

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// routes registers every API endpoint.
func (s *Server) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/orgDevices", s.listHandler(typeOrgDevices))
	mux.HandleFunc("GET /v1/orgDevices/{id}", s.getHandler(typeOrgDevices))
	mux.HandleFunc("GET /v1/orgDevices/{id}/appleCareCoverage", s.handleCoverage)
	mux.HandleFunc("GET /v1/orgDevices/{id}/relationships/assignedServer", s.handleAssignedServerLinkage)
	mux.HandleFunc("GET /v1/orgDevices/{id}/assignedServer", s.handleAssignedServer)
	mux.HandleFunc("GET /v1/mdmDevices", s.listHandler(typeMDMDevices))
	mux.HandleFunc("GET /v1/mdmDevices/{id}/details", s.handleMDMDeviceDetails)
	mux.HandleFunc("GET /v1/mdmServers", s.listHandler(typeMDMServers))
	mux.HandleFunc("POST /v1/mdmServers", s.handleCreateServer)
	mux.HandleFunc("GET /v1/mdmServers/{id}", s.getHandler(typeMDMServers))
	mux.HandleFunc("PATCH /v1/mdmServers/{id}", s.handleUpdateServer)
	mux.HandleFunc("DELETE /v1/mdmServers/{id}", s.handleDeleteServer)
	mux.HandleFunc("GET /v1/mdmServers/{id}/relationships/devices", s.handleServerDevices)
	mux.HandleFunc("POST /v1/orgDeviceActivities", s.handleCreateActivity)
	mux.HandleFunc("GET /v1/orgDeviceActivities/{id}", s.handleGetActivity)
	mux.HandleFunc("GET /v1/orgDeviceActivities/{id}/download", s.handleActivityLog)
	mux.HandleFunc("GET /v1/users", s.listHandler(typeUsers))
	mux.HandleFunc("GET /v1/users/{id}", s.getHandler(typeUsers))
	mux.HandleFunc("GET /v1/userGroups", s.listHandler(typeUserGroups))
	mux.HandleFunc("GET /v1/userGroups/{id}", s.getHandler(typeUserGroups))
	mux.HandleFunc("GET /v1/userGroups/{id}/relationships/users", s.usersLinkageHandler(typeUserGroups))
	mux.HandleFunc("GET /v1/organizationalUnits", s.listHandler(typeOrganizationalUnits))
	mux.HandleFunc("GET /v1/organizationalUnits/{id}", s.getHandler(typeOrganizationalUnits))
	mux.HandleFunc("GET /v1/organizationalUnits/{id}/relationships/users", s.usersLinkageHandler(typeOrganizationalUnits))
	mux.HandleFunc("GET /v1/apps", s.listHandler(typeApps))
	mux.HandleFunc("GET /v1/apps/{id}", s.getHandler(typeApps))
	mux.HandleFunc("GET /v1/packages", s.listHandler(typePackages))
	mux.HandleFunc("GET /v1/packages/{id}", s.getHandler(typePackages))
	mux.HandleFunc("GET /v1/configurations", s.handleListConfigurations)
	mux.HandleFunc("POST /v1/configurations", s.handleCreateConfiguration)
	mux.HandleFunc("GET /v1/configurations/{id}", s.getHandler(typeConfigurations))
	mux.HandleFunc("PATCH /v1/configurations/{id}", s.handleUpdateConfiguration)
	mux.HandleFunc("DELETE /v1/configurations/{id}", s.deleteHandler(typeConfigurations))
	mux.HandleFunc("GET /v1/blueprints", s.handleListBlueprints)
	mux.HandleFunc("POST /v1/blueprints", s.handleCreateBlueprint)
	mux.HandleFunc("GET /v1/blueprints/{id}", s.handleGetBlueprint)
	mux.HandleFunc("PATCH /v1/blueprints/{id}", s.handleUpdateBlueprint)
	mux.HandleFunc("DELETE /v1/blueprints/{id}", s.deleteHandler(typeBlueprints))
	mux.HandleFunc("GET /v1/blueprints/{id}/relationships/{rel}", s.handleBlueprintLinkages)
	mux.HandleFunc("POST /v1/blueprints/{id}/relationships/{rel}", s.handleBlueprintLink)
	mux.HandleFunc("DELETE /v1/blueprints/{id}/relationships/{rel}", s.handleBlueprintLink)
	mux.HandleFunc("GET /v1/auditEvents", s.handleAuditEvents)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.apiError(w, http.StatusNotFound, "PATH_ERROR.NOT_FOUND", "no such endpoint: "+r.Method+" "+r.URL.Path, nil)
	})
}

// listHandler lists a collection with fields and paging.
func (s *Server) listHandler(typ string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sel, ok := s.selectFields(w, r, typ)
		if !ok {
			return
		}
		limit, offset, ok := s.paging(w, r)
		if !ok {
			return
		}
		s.mu.Lock()
		s.refreshDeviceCounts(s.now())
		items := s.renderAll(s.store.byType(typ).all(), sel)
		s.mu.Unlock()
		s.writePage(w, r, items, limit, offset, nil)
	}
}

// getHandler returns one resource with fields.
func (s *Server) getHandler(typ string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sel, ok := s.selectFields(w, r, typ)
		if !ok {
			return
		}
		id := r.PathValue("id")
		s.mu.Lock()
		s.refreshDeviceCounts(s.now())
		res, found := s.store.byType(typ).get(id)
		var doc map[string]any
		if found {
			doc = s.render(res, sel)
		}
		s.mu.Unlock()
		if !found {
			s.notFound(w, typ, id)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": doc, "links": map[string]any{"self": s.URL + r.URL.RequestURI()}})
	}
}

// deleteHandler deletes one resource (204).
func (s *Server) deleteHandler(typ string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		s.mu.Lock()
		_, found := s.store.byType(typ).get(id)
		if found {
			s.store.byType(typ).del(id)
		}
		s.mu.Unlock()
		if !found {
			s.notFound(w, typ, id)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleCoverage is GET /v1/orgDevices/{id}/appleCareCoverage.
func (s *Server) handleCoverage(w http.ResponseWriter, r *http.Request) {
	sel, ok := s.selectFields(w, r, typeAppleCareCoverage)
	if !ok {
		return
	}
	limit, offset, ok := s.paging(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	s.mu.Lock()
	dev, found := s.store.devices.get(id)
	var items []any
	if found {
		cov, _ := dev.extra["coverage"].([]*resource)
		items = make([]any, 0, len(cov))
		for _, c := range cov {
			doc := s.render(c, sel)
			delete(doc, "links")
			items = append(items, doc)
		}
	}
	s.mu.Unlock()
	if !found {
		s.notFound(w, typeOrgDevices, id)
		return
	}
	s.writePage(w, r, items, limit, offset, nil)
}

// handleAssignedServerLinkage is GET
// /v1/orgDevices/{id}/relationships/assignedServer.
func (s *Server) handleAssignedServerLinkage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mu.Lock()
	_, found := s.store.devices.get(id)
	serverID := s.assignedServer(id, s.now())
	answer404 := s.unassigned404
	s.mu.Unlock()
	if !found || (serverID == "" && answer404) {
		s.notFound(w, typeOrgDevices, id)
		return
	}
	self := s.URL + "/v1/orgDevices/" + id
	writeJSON(w, http.StatusOK, map[string]any{
		"data":  map[string]any{"type": typeMDMServers, "id": serverID},
		"links": map[string]any{"self": self + "/relationships/assignedServer", "related": self + "/assignedServer"},
	})
}

// handleAssignedServer is GET /v1/orgDevices/{id}/assignedServer.
func (s *Server) handleAssignedServer(w http.ResponseWriter, r *http.Request) {
	sel, ok := s.selectFields(w, r, typeMDMServers)
	if !ok {
		return
	}
	id := r.PathValue("id")
	s.mu.Lock()
	_, found := s.store.devices.get(id)
	srv, assigned := s.store.servers.get(s.assignedServer(id, s.now()))
	var doc map[string]any
	if found && assigned {
		s.refreshDeviceCounts(s.now())
		doc = s.render(srv, sel)
	}
	s.mu.Unlock()
	if !found || !assigned {
		s.notFound(w, typeOrgDevices, id)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": doc, "links": map[string]any{"self": s.URL + r.URL.RequestURI()}})
}

// handleMDMDeviceDetails is GET /v1/mdmDevices/{id}/details.
func (s *Server) handleMDMDeviceDetails(w http.ResponseWriter, r *http.Request) {
	sel, ok := s.selectFields(w, r, typeMDMDeviceDetails)
	if !ok {
		return
	}
	id := r.PathValue("id")
	s.mu.Lock()
	dev, found := s.store.mdmDevices.get(id)
	var doc map[string]any
	if found {
		details, _ := dev.extra["details"].(map[string]any)
		doc = s.render(&resource{typ: typeMDMDeviceDetails, id: id, attrs: details}, sel)
	}
	s.mu.Unlock()
	if !found {
		s.notFound(w, typeMDMDevices, id)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": doc, "links": map[string]any{"self": s.URL + r.URL.RequestURI()}})
}

// serverRequest is the wire form of the MDM server create and update
// bodies.
type serverRequest struct {
	Data struct {
		Type       string `json:"type"`
		ID         string `json:"id"`
		Attributes struct {
			ServerName        string `json:"serverName"`
			ServerCertificate *struct {
				Name string `json:"name"`
				Data string `json:"data"`
			} `json:"serverCertificate"`
			EnableMDMDisownFlag    *bool    `json:"enableMdmDisownFlag"`
			DefaultProductFamilies []string `json:"defaultProductFamilies"`
		} `json:"attributes"`
	} `json:"data"`
}

// handleCreateServer is POST /v1/mdmServers.
func (s *Server) handleCreateServer(w http.ResponseWriter, r *http.Request) {
	var req serverRequest
	if err := readJSON(r, &req); err != nil || req.Data.Type != typeMDMServers {
		s.apiError(w, http.StatusBadRequest, "ENTITY_ERROR.ATTRIBUTE.INVALID", "body must be an mdmServers resource", map[string]any{"pointer": "/data/type"})
		return
	}
	if req.Data.Attributes.ServerName == "" || req.Data.Attributes.ServerCertificate == nil || req.Data.Attributes.ServerCertificate.Data == "" {
		s.apiError(w, http.StatusUnprocessableEntity, "ENTITY_ERROR.ATTRIBUTE.REQUIRED", "serverName and serverCertificate are required", map[string]any{"pointer": "/data/attributes"})
		return
	}
	s.mu.Lock()
	for _, srv := range s.store.servers.all() {
		if srv.attrs["serverName"] == req.Data.Attributes.ServerName {
			s.mu.Unlock()
			s.conflict(w, "ENTITY_ERROR.ATTRIBUTE.INVALID.DUPLICATE", "a device management service with that name exists", "/data/attributes/serverName")
			return
		}
	}
	s.mu.Unlock()
	disown := req.Data.Attributes.EnableMDMDisownFlag != nil && *req.Data.Attributes.EnableMDMDisownFlag
	id := s.AddMDMServer(req.Data.Attributes.ServerName, map[string]any{"enableMdmDisownFlag": disown})
	s.mu.Lock()
	srv, _ := s.store.servers.get(id)
	doc := s.render(srv, nil)
	s.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{"data": doc, "links": map[string]any{"self": s.URL + "/v1/mdmServers/" + id}})
}

// handleUpdateServer is PATCH /v1/mdmServers/{id}.
func (s *Server) handleUpdateServer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req serverRequest
	if err := readJSON(r, &req); err != nil || req.Data.Type != typeMDMServers || req.Data.ID != id {
		s.apiError(w, http.StatusBadRequest, "ENTITY_ERROR.ATTRIBUTE.INVALID", "body must be the mdmServers resource being updated", map[string]any{"pointer": "/data/id"})
		return
	}
	s.mu.Lock()
	srv, found := s.store.servers.get(id)
	var doc map[string]any
	if found {
		if req.Data.Attributes.ServerName != "" {
			srv.attrs["serverName"] = req.Data.Attributes.ServerName
		}
		if req.Data.Attributes.EnableMDMDisownFlag != nil {
			srv.attrs["enableMdmDisownFlag"] = *req.Data.Attributes.EnableMDMDisownFlag
		}
		if req.Data.Attributes.DefaultProductFamilies != nil {
			srv.attrs["defaultProductFamilies"] = req.Data.Attributes.DefaultProductFamilies
		}
		srv.attrs["updatedDateTime"] = s.now().UTC().Format(time.RFC3339Nano)
		doc = s.render(srv, nil)
	}
	s.mu.Unlock()
	if !found {
		s.notFound(w, typeMDMServers, id)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": doc, "links": map[string]any{"self": s.URL + r.URL.RequestURI()}})
}

// handleDeleteServer is DELETE /v1/mdmServers/{id}; 409 while devices are
// assigned.
func (s *Server) handleDeleteServer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mu.Lock()
	_, found := s.store.servers.get(id)
	assigned := 0
	for _, a := range s.store.assignments {
		if a.serverID == id {
			assigned++
		}
	}
	if found && assigned == 0 {
		s.store.servers.del(id)
	}
	s.mu.Unlock()
	switch {
	case !found:
		s.notFound(w, typeMDMServers, id)
	case assigned > 0:
		s.conflict(w, "ENTITY_ERROR.STATE", "a device management service with assigned devices cannot be deleted", "")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleServerDevices is GET /v1/mdmServers/{id}/relationships/devices.
func (s *Server) handleServerDevices(w http.ResponseWriter, r *http.Request) {
	limit, offset, ok := s.paging(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	s.mu.Lock()
	_, found := s.store.servers.get(id)
	items := linkages(typeOrgDevices, s.serverDevices(id, s.now()))
	s.mu.Unlock()
	if !found {
		s.notFound(w, typeMDMServers, id)
		return
	}
	s.writePage(w, r, items, limit, offset, nil)
}

// usersLinkageHandler serves relationships/users of groups and units.
func (s *Server) usersLinkageHandler(typ string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, offset, ok := s.paging(w, r)
		if !ok {
			return
		}
		id := r.PathValue("id")
		s.mu.Lock()
		res, found := s.store.byType(typ).get(id)
		var items []any
		if found {
			items = linkages(typeUsers, res.rels["users"])
		}
		s.mu.Unlock()
		if !found {
			s.notFound(w, typ, id)
			return
		}
		s.writePage(w, r, items, limit, offset, nil)
	}
}

// handleListConfigurations is GET /v1/configurations; customSettingsValues
// is null in the list.
func (s *Server) handleListConfigurations(w http.ResponseWriter, r *http.Request) {
	sel, ok := s.selectFields(w, r, typeConfigurations)
	if !ok {
		return
	}
	limit, offset, ok := s.paging(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	items := s.renderAll(s.store.configurations.all(), sel)
	for _, item := range items {
		attrs, _ := item.(map[string]any)["attributes"].(map[string]any)
		if _, has := attrs["customSettingsValues"]; has {
			attrs["customSettingsValues"] = nil
		}
	}
	s.mu.Unlock()
	s.writePage(w, r, items, limit, offset, nil)
}

// configurationRequest is the wire form of the configuration create and
// update bodies.
type configurationRequest struct {
	Data struct {
		Type       string `json:"type"`
		ID         string `json:"id"`
		Attributes struct {
			Type                   string   `json:"type"`
			Name                   string   `json:"name"`
			ConfiguredForPlatforms []string `json:"configuredForPlatforms"`
			CustomSettingsValues   *struct {
				ConfigurationProfile string `json:"configurationProfile"`
				Filename             string `json:"filename"`
			} `json:"customSettingsValues"`
		} `json:"attributes"`
	} `json:"data"`
}

// handleCreateConfiguration is POST /v1/configurations.
func (s *Server) handleCreateConfiguration(w http.ResponseWriter, r *http.Request) {
	var req configurationRequest
	if err := readJSON(r, &req); err != nil || req.Data.Type != typeConfigurations {
		s.apiError(w, http.StatusBadRequest, "ENTITY_ERROR.ATTRIBUTE.INVALID", "body must be a configurations resource", map[string]any{"pointer": "/data/type"})
		return
	}
	a := req.Data.Attributes
	switch {
	case a.Type != "CUSTOM_SETTING":
		s.apiError(w, http.StatusUnprocessableEntity, "ENTITY_ERROR.ATTRIBUTE.INVALID", "only CUSTOM_SETTING configurations can be created", map[string]any{"pointer": "/data/attributes/type"})
		return
	case a.CustomSettingsValues == nil || a.CustomSettingsValues.ConfigurationProfile == "":
		s.apiError(w, http.StatusUnprocessableEntity, "ENTITY_ERROR.ATTRIBUTE.REQUIRED", "configurationProfile is required", map[string]any{"pointer": "/data/attributes/customSettingsValues/configurationProfile"})
		return
	case a.CustomSettingsValues.Filename != "" && !strings.HasSuffix(a.CustomSettingsValues.Filename, ".mobileconfig"):
		s.apiError(w, http.StatusUnprocessableEntity, "ENTITY_ERROR.ATTRIBUTE.INVALID", "filename must end in .mobileconfig", map[string]any{"pointer": "/data/attributes/customSettingsValues/filename"})
		return
	}
	id := "cfg-" + randomHex(6)
	filename := a.CustomSettingsValues.Filename
	if filename == "" {
		filename = id + ".mobileconfig"
	}
	platforms := a.ConfiguredForPlatforms
	if len(platforms) == 0 {
		platforms = []string{"PLATFORM_MACOS"}
	}
	s.AddConfiguration(id, map[string]any{
		"type": a.Type, "name": a.Name, "configuredForPlatforms": platforms,
		"customSettingsValues": map[string]any{"configurationProfile": a.CustomSettingsValues.ConfigurationProfile, "filename": filename},
	})
	s.mu.Lock()
	res, _ := s.store.configurations.get(id)
	doc := s.render(res, nil)
	s.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{"data": doc, "links": map[string]any{"self": s.URL + "/v1/configurations/" + id}})
}

// handleUpdateConfiguration is PATCH /v1/configurations/{id}.
func (s *Server) handleUpdateConfiguration(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req configurationRequest
	if err := readJSON(r, &req); err != nil || req.Data.Type != typeConfigurations || req.Data.ID != id {
		s.apiError(w, http.StatusBadRequest, "ENTITY_ERROR.ATTRIBUTE.INVALID", "body must be the configurations resource being updated", map[string]any{"pointer": "/data/id"})
		return
	}
	a := req.Data.Attributes
	if a.Name == "" && len(a.ConfiguredForPlatforms) == 0 && a.CustomSettingsValues == nil {
		s.apiError(w, http.StatusUnprocessableEntity, "ENTITY_ERROR.ATTRIBUTE.REQUIRED", "one of name, configuredForPlatforms, configurationProfile, or filename is required", map[string]any{"pointer": "/data/attributes"})
		return
	}
	if a.CustomSettingsValues != nil && a.CustomSettingsValues.Filename != "" && !strings.HasSuffix(a.CustomSettingsValues.Filename, ".mobileconfig") {
		s.apiError(w, http.StatusUnprocessableEntity, "ENTITY_ERROR.ATTRIBUTE.INVALID", "filename must end in .mobileconfig", map[string]any{"pointer": "/data/attributes/customSettingsValues/filename"})
		return
	}
	s.mu.Lock()
	res, found := s.store.configurations.get(id)
	var doc map[string]any
	if found {
		if res.attrs["type"] != "CUSTOM_SETTING" {
			s.mu.Unlock()
			s.conflict(w, "ENTITY_ERROR.STATE", "only CUSTOM_SETTING configurations can be updated", "/data/attributes/type")
			return
		}
		if a.Name != "" {
			res.attrs["name"] = a.Name
		}
		if len(a.ConfiguredForPlatforms) > 0 {
			res.attrs["configuredForPlatforms"] = a.ConfiguredForPlatforms
		}
		if a.CustomSettingsValues != nil {
			values, _ := res.attrs["customSettingsValues"].(map[string]any)
			if values == nil {
				values = map[string]any{}
			}
			if a.CustomSettingsValues.ConfigurationProfile != "" {
				values["configurationProfile"] = a.CustomSettingsValues.ConfigurationProfile
			}
			if a.CustomSettingsValues.Filename != "" {
				values["filename"] = a.CustomSettingsValues.Filename
			}
			res.attrs["customSettingsValues"] = values
		}
		res.attrs["updatedDateTime"] = s.now().UTC().Format(time.RFC3339Nano)
		doc = s.render(res, nil)
	}
	s.mu.Unlock()
	if !found {
		s.notFound(w, typeConfigurations, id)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": doc, "links": map[string]any{"self": s.URL + r.URL.RequestURI()}})
}

// blueprintInclude parses include and limit[rel], or writes 400.
func (s *Server) blueprintInclude(w http.ResponseWriter, r *http.Request) (include []string, limits map[string]int, ok bool) {
	q := r.URL.Query()
	if raw := q.Get("include"); raw != "" {
		for _, rel := range strings.Split(raw, ",") {
			rel = strings.TrimSpace(rel)
			if !slices.Contains(blueprintRelationships, rel) {
				s.badParameter(w, "include", "'"+rel+"' is not a valid include")
				return nil, nil, false
			}
			include = append(include, rel)
		}
	}
	limits = map[string]int{}
	for _, rel := range blueprintRelationships {
		name := "limit[" + rel + "]"
		if v := q.Get(name); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > MaxLimit {
				s.badParameter(w, name, name+" must be an integer between 1 and 1000")
				return nil, nil, false
			}
			limits[rel] = n
		}
	}
	return include, limits, true
}

// includedFor renders the included resources of blueprints.
func (s *Server) includedFor(bps []*resource, include []string, limits map[string]int) []any {
	if len(include) == 0 {
		return nil
	}
	out := []any{}
	seen := map[string]struct{}{}
	for _, bp := range bps {
		for _, rel := range include {
			ids := bp.rels[rel]
			if n, ok := limits[rel]; ok && n < len(ids) {
				ids = ids[:n]
			}
			col := s.store.byType(relationshipTypes[rel])
			for _, id := range ids {
				if _, dup := seen[rel+"/"+id]; dup {
					continue
				}
				if res, ok := col.get(id); ok {
					seen[rel+"/"+id] = struct{}{}
					out = append(out, s.render(res, nil))
				}
			}
		}
	}
	return out
}

// handleListBlueprints is GET /v1/blueprints.
func (s *Server) handleListBlueprints(w http.ResponseWriter, r *http.Request) {
	sel, ok := s.selectFields(w, r, typeBlueprints)
	if !ok {
		return
	}
	limit, offset, ok := s.paging(w, r)
	if !ok {
		return
	}
	include, limits, ok := s.blueprintInclude(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	bps := s.store.blueprints.all()
	items := s.renderAll(bps, sel)
	included := s.includedFor(bps, include, limits)
	s.mu.Unlock()
	s.writePage(w, r, items, limit, offset, included)
}

// handleGetBlueprint is GET /v1/blueprints/{id}.
func (s *Server) handleGetBlueprint(w http.ResponseWriter, r *http.Request) {
	sel, ok := s.selectFields(w, r, typeBlueprints)
	if !ok {
		return
	}
	include, limits, ok := s.blueprintInclude(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	s.mu.Lock()
	bp, found := s.store.blueprints.get(id)
	var doc map[string]any
	var included []any
	if found {
		doc = s.render(bp, sel)
		included = s.includedFor([]*resource{bp}, include, limits)
	}
	s.mu.Unlock()
	if !found {
		s.notFound(w, typeBlueprints, id)
		return
	}
	out := map[string]any{"data": doc, "links": map[string]any{"self": s.URL + r.URL.RequestURI()}}
	if included != nil {
		out["included"] = included
	}
	writeJSON(w, http.StatusOK, out)
}

// blueprintRequest is the wire form of the blueprint create and update
// bodies.
type blueprintRequest struct {
	Data struct {
		Type       string `json:"type"`
		ID         string `json:"id"`
		Attributes *struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"attributes"`
		Relationships map[string]struct {
			Data []struct {
				Type string `json:"type"`
				ID   string `json:"id"`
			} `json:"data"`
		} `json:"relationships"`
	} `json:"data"`
}

// applyRelationships sets the blueprint's relationships from the request,
// dropping ids that do not exist (as Apple documents).
func (s *Server) applyRelationships(bp *resource, req blueprintRequest) {
	for rel, body := range req.Data.Relationships {
		typ, ok := relationshipTypes[rel]
		if !ok {
			continue
		}
		col := s.store.byType(typ)
		var ids []string
		for _, l := range body.Data {
			if _, exists := col.get(l.ID); exists {
				ids = append(ids, l.ID)
			}
		}
		bp.rels[rel] = ids
	}
}

// handleCreateBlueprint is POST /v1/blueprints.
func (s *Server) handleCreateBlueprint(w http.ResponseWriter, r *http.Request) {
	var req blueprintRequest
	if err := readJSON(r, &req); err != nil || req.Data.Type != typeBlueprints {
		s.apiError(w, http.StatusBadRequest, "ENTITY_ERROR.ATTRIBUTE.INVALID", "body must be a blueprints resource", map[string]any{"pointer": "/data/type"})
		return
	}
	if req.Data.Attributes == nil || req.Data.Attributes.Name == "" {
		s.apiError(w, http.StatusUnprocessableEntity, "ENTITY_ERROR.ATTRIBUTE.REQUIRED", "name is required", map[string]any{"pointer": "/data/attributes/name"})
		return
	}
	id := "bp-" + randomHex(6)
	s.AddBlueprint(id, map[string]any{"name": req.Data.Attributes.Name, "description": req.Data.Attributes.Description})
	s.mu.Lock()
	bp, _ := s.store.blueprints.get(id)
	s.applyRelationships(bp, req)
	doc := s.render(bp, nil)
	s.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{"data": doc, "links": map[string]any{"self": s.URL + "/v1/blueprints/" + id}})
}

// handleUpdateBlueprint is PATCH /v1/blueprints/{id}.
func (s *Server) handleUpdateBlueprint(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req blueprintRequest
	if err := readJSON(r, &req); err != nil || req.Data.Type != typeBlueprints || req.Data.ID != id {
		s.apiError(w, http.StatusBadRequest, "ENTITY_ERROR.ATTRIBUTE.INVALID", "body must be the blueprints resource being updated", map[string]any{"pointer": "/data/id"})
		return
	}
	s.mu.Lock()
	bp, found := s.store.blueprints.get(id)
	var doc map[string]any
	if found {
		if req.Data.Attributes != nil {
			if req.Data.Attributes.Name != "" {
				bp.attrs["name"] = req.Data.Attributes.Name
			}
			if req.Data.Attributes.Description != "" {
				bp.attrs["description"] = req.Data.Attributes.Description
			}
		}
		s.applyRelationships(bp, req)
		bp.attrs["updatedDateTime"] = s.now().UTC().Format(time.RFC3339Nano)
		doc = s.render(bp, nil)
	}
	s.mu.Unlock()
	if !found {
		s.notFound(w, typeBlueprints, id)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": doc, "links": map[string]any{"self": s.URL + r.URL.RequestURI()}})
}

// handleBlueprintLinkages is GET /v1/blueprints/{id}/relationships/{rel}.
func (s *Server) handleBlueprintLinkages(w http.ResponseWriter, r *http.Request) {
	limit, offset, ok := s.paging(w, r)
	if !ok {
		return
	}
	id, rel := r.PathValue("id"), r.PathValue("rel")
	typ, known := relationshipTypes[rel]
	s.mu.Lock()
	bp, found := s.store.blueprints.get(id)
	var items []any
	if found && known {
		items = linkages(typ, bp.rels[rel])
	}
	s.mu.Unlock()
	if !found || !known {
		s.notFound(w, typeBlueprints, id+"/relationships/"+rel)
		return
	}
	s.writePage(w, r, items, limit, offset, nil)
}

// handleBlueprintLink is POST and DELETE
// /v1/blueprints/{id}/relationships/{rel}.
func (s *Server) handleBlueprintLink(w http.ResponseWriter, r *http.Request) {
	id, rel := r.PathValue("id"), r.PathValue("rel")
	typ, known := relationshipTypes[rel]
	if !known {
		s.notFound(w, typeBlueprints, id+"/relationships/"+rel)
		return
	}
	var req struct {
		Data []struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		} `json:"data"`
	}
	if err := readJSON(r, &req); err != nil || len(req.Data) == 0 {
		s.apiError(w, http.StatusUnprocessableEntity, "ENTITY_ERROR.RELATIONSHIP.REQUIRED", "data must list at least one linkage", map[string]any{"pointer": "/data"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	bp, found := s.store.blueprints.get(id)
	if !found {
		s.notFound(w, typeBlueprints, id)
		return
	}
	col := s.store.byType(typ)
	for _, l := range req.Data {
		if l.Type != typ {
			s.apiError(w, http.StatusUnprocessableEntity, "ENTITY_ERROR.RELATIONSHIP.INVALID", "linkage type must be "+typ, map[string]any{"pointer": "/data/type"})
			return
		}
		if _, exists := col.get(l.ID); !exists {
			s.conflict(w, "ENTITY_ERROR.RELATIONSHIP.INVALID", "no "+typ+" with id "+l.ID, "/data/id")
			return
		}
	}
	for _, l := range req.Data {
		switch r.Method {
		case http.MethodPost:
			if !slices.Contains(bp.rels[rel], l.ID) {
				bp.rels[rel] = append(bp.rels[rel], l.ID)
			}
		default:
			bp.rels[rel] = slices.DeleteFunc(bp.rels[rel], func(v string) bool { return v == l.ID })
		}
	}
	bp.attrs["updatedDateTime"] = s.now().UTC().Format(time.RFC3339Nano)
	w.WriteHeader(http.StatusNoContent)
}

// handleAuditEvents is GET /v1/auditEvents.
func (s *Server) handleAuditEvents(w http.ResponseWriter, r *http.Request) {
	sel, ok := s.selectFields(w, r, typeAuditEvents)
	if !ok {
		return
	}
	limit, offset, ok := s.paging(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	start, err := time.Parse(time.RFC3339, q.Get("filter[startTimestamp]"))
	if err != nil {
		s.badParameter(w, "filter[startTimestamp]", "filter[startTimestamp] is required in ISO 8601 form")
		return
	}
	end, err := time.Parse(time.RFC3339, q.Get("filter[endTimestamp]"))
	if err != nil {
		s.badParameter(w, "filter[endTimestamp]", "filter[endTimestamp] is required in ISO 8601 form")
		return
	}
	actor, subject, typ := q.Get("filter[actorId]"), q.Get("filter[subjectId]"), q.Get("filter[type]")
	s.mu.Lock()
	var matched []*resource
	for _, ev := range s.store.audits.all() {
		at, _ := time.Parse(time.RFC3339Nano, ev.attrs["eventDateTime"].(string)) //nolint:forcetypeassert,errcheck // seeded as time
		if at.Before(start) || at.After(end) {
			continue
		}
		if actor != "" && ev.attrs["actorId"] != actor {
			continue
		}
		if subject != "" && ev.attrs["subjectId"] != subject {
			continue
		}
		if typ != "" && ev.attrs["type"] != typ {
			continue
		}
		matched = append(matched, ev)
	}
	items := make([]any, 0, len(matched))
	for _, ev := range matched {
		doc := s.render(ev, sel)
		delete(doc, "links")
		items = append(items, doc)
	}
	s.mu.Unlock()
	s.writePage(w, r, items, limit, offset, nil)
}
