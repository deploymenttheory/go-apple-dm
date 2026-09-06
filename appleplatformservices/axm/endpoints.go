package axm

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// requireID rejects an empty path parameter before a request is built.
func requireID(what, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: %s is required", ErrArgument, what)
	}
	return nil
}

// ListOrgDevices lists the organization's devices (GET /v1/orgDevices).
func (c *Client) ListOrgDevices(ctx context.Context, o ListOptions) (Page[OrgDevice], error) {
	q, err := o.query(TypeOrgDevices)
	if err != nil {
		return Page[OrgDevice]{}, err
	}
	return list[OrgDevice](ctx, c, "/v1/orgDevices", q)
}

// GetOrgDevice returns one device by serial number (GET /v1/orgDevices/{id}).
func (c *Client) GetOrgDevice(ctx context.Context, id string, o GetOptions) (*OrgDevice, error) {
	if err := requireID("device id", id); err != nil {
		return nil, err
	}
	return get[OrgDevice](ctx, c, "/v1/orgDevices/"+url.PathEscape(id), o.query(TypeOrgDevices))
}

// ListAppleCareCoverage lists a device's AppleCare coverages
// (GET /v1/orgDevices/{id}/appleCareCoverage).
func (c *Client) ListAppleCareCoverage(ctx context.Context, id string, o ListOptions) (Page[AppleCareCoverage], error) {
	if err := requireID("device id", id); err != nil {
		return Page[AppleCareCoverage]{}, err
	}
	q, err := o.query(TypeAppleCareCoverage)
	if err != nil {
		return Page[AppleCareCoverage]{}, err
	}
	return list[AppleCareCoverage](ctx, c, "/v1/orgDevices/"+url.PathEscape(id)+"/appleCareCoverage", q)
}

// ListMDMDevices lists devices enrolled in Apple's built-in device
// management (GET /v1/mdmDevices).
func (c *Client) ListMDMDevices(ctx context.Context, o ListOptions) (Page[MDMDevice], error) {
	q, err := o.query(TypeMDMDevices)
	if err != nil {
		return Page[MDMDevice]{}, err
	}
	return list[MDMDevice](ctx, c, "/v1/mdmDevices", q)
}

// GetMDMDeviceDetails returns the detailed view of an MDM-enrolled device
// (GET /v1/mdmDevices/{id}/details).
func (c *Client) GetMDMDeviceDetails(ctx context.Context, id string, o GetOptions) (*MDMDeviceDetail, error) {
	if err := requireID("device id", id); err != nil {
		return nil, err
	}
	return get[MDMDeviceDetail](ctx, c, "/v1/mdmDevices/"+url.PathEscape(id)+"/details", o.query(TypeMDMDeviceDetails))
}

// ListMDMServers lists the device management services (GET /v1/mdmServers).
func (c *Client) ListMDMServers(ctx context.Context, o ListOptions) (Page[MDMServer], error) {
	q, err := o.query(TypeMDMServers)
	if err != nil {
		return Page[MDMServer]{}, err
	}
	return list[MDMServer](ctx, c, "/v1/mdmServers", q)
}

// GetMDMServer returns one device management service (GET /v1/mdmServers/{id}).
func (c *Client) GetMDMServer(ctx context.Context, id string, o GetOptions) (*MDMServer, error) {
	if err := requireID("server id", id); err != nil {
		return nil, err
	}
	return get[MDMServer](ctx, c, "/v1/mdmServers/"+url.PathEscape(id), o.query(TypeMDMServers))
}

// CreateMDMServer creates a device management service (POST /v1/mdmServers,
// 201). ServerName and ServerCertificate are required.
func (c *Client) CreateMDMServer(ctx context.Context, attrs MDMServerCreateAttributes, opts ...RequestOption) (*MDMServer, error) {
	if attrs.ServerName == "" || attrs.ServerCertificate.Data == "" {
		return nil, fmt.Errorf("%w: serverName and serverCertificate are required", ErrArgument)
	}
	body := MDMServerCreateRequest{Data: MDMServerCreateData{Type: TypeMDMServers, Attributes: attrs}}
	var doc single[MDMServer]
	if err := c.do(ctx, request{method: http.MethodPost, path: "/v1/mdmServers", body: body, opts: options(opts)}, &doc); err != nil {
		return nil, err
	}
	return &doc.Data, nil
}

// UpdateMDMServer updates a device management service (PATCH
// /v1/mdmServers/{id}); omitted attributes keep their value.
func (c *Client) UpdateMDMServer(ctx context.Context, id string, attrs MDMServerUpdateAttributes) (*MDMServer, error) {
	if err := requireID("server id", id); err != nil {
		return nil, err
	}
	body := MDMServerUpdateRequest{Data: MDMServerUpdateData{Type: TypeMDMServers, ID: id, Attributes: attrs}}
	var doc single[MDMServer]
	if err := c.do(ctx, request{method: http.MethodPatch, path: "/v1/mdmServers/" + url.PathEscape(id), body: body}, &doc); err != nil {
		return nil, err
	}
	return &doc.Data, nil
}

// DeleteMDMServer deletes a device management service (DELETE
// /v1/mdmServers/{id}, 204). Apple answers 409 while devices are assigned.
func (c *Client) DeleteMDMServer(ctx context.Context, id string) error {
	if err := requireID("server id", id); err != nil {
		return err
	}
	return c.do(ctx, request{method: http.MethodDelete, path: "/v1/mdmServers/" + url.PathEscape(id)}, nil)
}

// ListMDMServerDevices lists the serial numbers assigned to a device
// management service (GET /v1/mdmServers/{id}/relationships/devices).
func (c *Client) ListMDMServerDevices(ctx context.Context, id string, o ListOptions) (Page[Linkage], error) {
	if err := requireID("server id", id); err != nil {
		return Page[Linkage]{}, err
	}
	q, err := o.query(TypeMDMServers)
	if err != nil {
		return Page[Linkage]{}, err
	}
	return list[Linkage](ctx, c, "/v1/mdmServers/"+url.PathEscape(id)+"/relationships/devices", q)
}

// GetAssignedServerLinkage returns the id of the device management
// service a device is assigned to (GET
// /v1/orgDevices/{id}/relationships/assignedServer). An unassigned device
// yields an empty ID, or a 404 from Apple.
func (c *Client) GetAssignedServerLinkage(ctx context.Context, id string) (*Linkage, error) {
	if err := requireID("device id", id); err != nil {
		return nil, err
	}
	var doc single[*Linkage]
	if err := c.do(ctx, request{method: http.MethodGet, path: "/v1/orgDevices/" + url.PathEscape(id) + "/relationships/assignedServer"}, &doc); err != nil {
		return nil, err
	}
	if doc.Data == nil {
		return &Linkage{}, nil
	}
	return doc.Data, nil
}

// GetAssignedServer returns the device management service a device is
// assigned to (GET /v1/orgDevices/{id}/assignedServer).
func (c *Client) GetAssignedServer(ctx context.Context, id string, o GetOptions) (*MDMServer, error) {
	if err := requireID("device id", id); err != nil {
		return nil, err
	}
	return get[MDMServer](ctx, c, "/v1/orgDevices/"+url.PathEscape(id)+"/assignedServer", o.query(TypeMDMServers))
}

// CreateOrgDeviceActivity submits a device management activity (POST
// /v1/orgDeviceActivities, 201). The workflow methods build the request
// with Apple's rules checked; this method sends it as given.
func (c *Client) CreateOrgDeviceActivity(ctx context.Context, req OrgDeviceActivityCreateRequest, opts ...RequestOption) (*OrgDeviceActivity, error) {
	if req.Data.Type == "" {
		req.Data.Type = TypeOrgDeviceActivities
	}
	if req.Data.Attributes.ActivityType == "" {
		return nil, fmt.Errorf("%w: activityType is required", ErrArgument)
	}
	var doc single[OrgDeviceActivity]
	if err := c.do(ctx, request{method: http.MethodPost, path: "/v1/orgDeviceActivities", body: req, opts: options(opts)}, &doc); err != nil {
		return nil, err
	}
	return &doc.Data, nil
}

// GetOrgDeviceActivity returns an activity (GET /v1/orgDeviceActivities/{id}).
func (c *Client) GetOrgDeviceActivity(ctx context.Context, id string, o GetOptions) (*OrgDeviceActivity, error) {
	if err := requireID("activity id", id); err != nil {
		return nil, err
	}
	return get[OrgDeviceActivity](ctx, c, "/v1/orgDeviceActivities/"+url.PathEscape(id), o.query(TypeOrgDeviceActivities))
}

// ListUsers lists the organization's users (GET /v1/users).
func (c *Client) ListUsers(ctx context.Context, o ListOptions) (Page[User], error) {
	q, err := o.query(TypeUsers)
	if err != nil {
		return Page[User]{}, err
	}
	return list[User](ctx, c, "/v1/users", q)
}

// GetUser returns one user (GET /v1/users/{id}).
func (c *Client) GetUser(ctx context.Context, id string, o GetOptions) (*User, error) {
	if err := requireID("user id", id); err != nil {
		return nil, err
	}
	return get[User](ctx, c, "/v1/users/"+url.PathEscape(id), o.query(TypeUsers))
}

// ListUserGroups lists the organization's user groups (GET /v1/userGroups).
func (c *Client) ListUserGroups(ctx context.Context, o ListOptions) (Page[UserGroup], error) {
	q, err := o.query(TypeUserGroups)
	if err != nil {
		return Page[UserGroup]{}, err
	}
	return list[UserGroup](ctx, c, "/v1/userGroups", q)
}

// GetUserGroup returns one user group (GET /v1/userGroups/{id}).
func (c *Client) GetUserGroup(ctx context.Context, id string, o GetOptions) (*UserGroup, error) {
	if err := requireID("user group id", id); err != nil {
		return nil, err
	}
	return get[UserGroup](ctx, c, "/v1/userGroups/"+url.PathEscape(id), o.query(TypeUserGroups))
}

// ListUserGroupUsers lists the user ids of a user group (GET
// /v1/userGroups/{id}/relationships/users).
func (c *Client) ListUserGroupUsers(ctx context.Context, id string, o ListOptions) (Page[Linkage], error) {
	if err := requireID("user group id", id); err != nil {
		return Page[Linkage]{}, err
	}
	q, err := o.query(TypeUsers)
	if err != nil {
		return Page[Linkage]{}, err
	}
	return list[Linkage](ctx, c, "/v1/userGroups/"+url.PathEscape(id)+"/relationships/users", q)
}

// ListOrganizationalUnits lists the organizational units (GET
// /v1/organizationalUnits).
func (c *Client) ListOrganizationalUnits(ctx context.Context, o ListOptions) (Page[OrganizationalUnit], error) {
	q, err := o.query(TypeOrganizationalUnits)
	if err != nil {
		return Page[OrganizationalUnit]{}, err
	}
	return list[OrganizationalUnit](ctx, c, "/v1/organizationalUnits", q)
}

// GetOrganizationalUnit returns one organizational unit (GET
// /v1/organizationalUnits/{id}).
func (c *Client) GetOrganizationalUnit(ctx context.Context, id string, o GetOptions) (*OrganizationalUnit, error) {
	if err := requireID("organizational unit id", id); err != nil {
		return nil, err
	}
	return get[OrganizationalUnit](ctx, c, "/v1/organizationalUnits/"+url.PathEscape(id), o.query(TypeOrganizationalUnits))
}

// ListOrganizationalUnitUsers lists the user ids of an organizational unit
// (GET /v1/organizationalUnits/{id}/relationships/users).
func (c *Client) ListOrganizationalUnitUsers(ctx context.Context, id string, o ListOptions) (Page[Linkage], error) {
	if err := requireID("organizational unit id", id); err != nil {
		return Page[Linkage]{}, err
	}
	q, err := o.query(TypeUsers)
	if err != nil {
		return Page[Linkage]{}, err
	}
	return list[Linkage](ctx, c, "/v1/organizationalUnits/"+url.PathEscape(id)+"/relationships/users", q)
}

// ListApps lists the apps of the built-in device management (GET /v1/apps).
func (c *Client) ListApps(ctx context.Context, o ListOptions) (Page[App], error) {
	q, err := o.query(TypeApps)
	if err != nil {
		return Page[App]{}, err
	}
	return list[App](ctx, c, "/v1/apps", q)
}

// GetApp returns one app (GET /v1/apps/{id}).
func (c *Client) GetApp(ctx context.Context, id string, o GetOptions) (*App, error) {
	if err := requireID("app id", id); err != nil {
		return nil, err
	}
	return get[App](ctx, c, "/v1/apps/"+url.PathEscape(id), o.query(TypeApps))
}

// ListPackages lists the packages of the built-in device management (GET
// /v1/packages).
func (c *Client) ListPackages(ctx context.Context, o ListOptions) (Page[Package], error) {
	q, err := o.query(TypePackages)
	if err != nil {
		return Page[Package]{}, err
	}
	return list[Package](ctx, c, "/v1/packages", q)
}

// GetPackage returns one package (GET /v1/packages/{id}).
func (c *Client) GetPackage(ctx context.Context, id string, o GetOptions) (*Package, error) {
	if err := requireID("package id", id); err != nil {
		return nil, err
	}
	return get[Package](ctx, c, "/v1/packages/"+url.PathEscape(id), o.query(TypePackages))
}

// ListConfigurations lists the configurations (GET /v1/configurations);
// customSettingsValues is null in this view.
func (c *Client) ListConfigurations(ctx context.Context, o ListOptions) (Page[Configuration], error) {
	q, err := o.query(TypeConfigurations)
	if err != nil {
		return Page[Configuration]{}, err
	}
	return list[Configuration](ctx, c, "/v1/configurations", q)
}

// GetConfiguration returns one configuration with its customSettingsValues
// (GET /v1/configurations/{id}).
func (c *Client) GetConfiguration(ctx context.Context, id string, o GetOptions) (*Configuration, error) {
	if err := requireID("configuration id", id); err != nil {
		return nil, err
	}
	return get[Configuration](ctx, c, "/v1/configurations/"+url.PathEscape(id), o.query(TypeConfigurations))
}

// CreateConfiguration creates a CUSTOM_SETTING configuration (POST
// /v1/configurations, 201). The type is set when empty.
func (c *Client) CreateConfiguration(ctx context.Context, attrs ConfigurationCreateAttributes, opts ...RequestOption) (*Configuration, error) {
	if attrs.Type == "" {
		attrs.Type = ConfigurationCustomSetting
	}
	if attrs.CustomSettingsValues.ConfigurationProfile == "" {
		return nil, fmt.Errorf("%w: configurationProfile is required", ErrArgument)
	}
	body := ConfigurationCreateRequest{Data: ConfigurationCreateData{Type: TypeConfigurations, Attributes: attrs}}
	var doc single[Configuration]
	if err := c.do(ctx, request{method: http.MethodPost, path: "/v1/configurations", body: body, opts: options(opts)}, &doc); err != nil {
		return nil, err
	}
	return &doc.Data, nil
}

// UpdateConfiguration updates a CUSTOM_SETTING configuration (PATCH
// /v1/configurations/{id}); at least one attribute must be set.
func (c *Client) UpdateConfiguration(ctx context.Context, id string, attrs ConfigurationUpdateAttributes) (*Configuration, error) {
	if err := requireID("configuration id", id); err != nil {
		return nil, err
	}
	if attrs.Name == "" && len(attrs.ConfiguredForPlatforms) == 0 && attrs.CustomSettingsValues == nil {
		return nil, fmt.Errorf("%w: one of name, configuredForPlatforms, or customSettingsValues is required", ErrArgument)
	}
	body := ConfigurationUpdateRequest{Data: ConfigurationUpdateData{Type: TypeConfigurations, ID: id, Attributes: attrs}}
	var doc single[Configuration]
	if err := c.do(ctx, request{method: http.MethodPatch, path: "/v1/configurations/" + url.PathEscape(id), body: body}, &doc); err != nil {
		return nil, err
	}
	return &doc.Data, nil
}

// DeleteConfiguration deletes a configuration (DELETE
// /v1/configurations/{id}, 204).
func (c *Client) DeleteConfiguration(ctx context.Context, id string) error {
	if err := requireID("configuration id", id); err != nil {
		return err
	}
	return c.do(ctx, request{method: http.MethodDelete, path: "/v1/configurations/" + url.PathEscape(id)}, nil)
}

// BlueprintInclude asks for related resources in a blueprint response:
// Include lists the relationships (include=…), Limits caps each
// (limit[apps]=…).
type BlueprintInclude struct {
	Include []BlueprintRelationship
	Limits  map[BlueprintRelationship]int
}

// BlueprintListOptions are the query parameters of ListBlueprints.
type BlueprintListOptions struct {
	ListOptions
	BlueprintInclude
}

// BlueprintGetOptions are the query parameters of GetBlueprint.
type BlueprintGetOptions struct {
	GetOptions
	BlueprintInclude
}

// apply adds the include parameters to q.
func (b BlueprintInclude) apply(q url.Values) error {
	if len(b.Include) > 0 {
		parts := make([]string, len(b.Include))
		for i, rel := range b.Include {
			parts[i] = string(rel)
		}
		q.Set("include", strings.Join(parts, ","))
	}
	for _, rel := range BlueprintRelationships {
		n, ok := b.Limits[rel]
		if !ok {
			continue
		}
		if n < 1 || n > MaxLimit {
			return fmt.Errorf("%w: limit[%s] %d", ErrLimit, rel, n)
		}
		q.Set("limit["+string(rel)+"]", strconv.Itoa(n))
	}
	return nil
}

// ListBlueprints lists the blueprints (GET /v1/blueprints).
func (c *Client) ListBlueprints(ctx context.Context, o BlueprintListOptions) (Page[Blueprint], error) {
	q, err := o.query(TypeBlueprints)
	if err != nil {
		return Page[Blueprint]{}, err
	}
	if err := o.apply(q); err != nil {
		return Page[Blueprint]{}, err
	}
	return list[Blueprint](ctx, c, "/v1/blueprints", q)
}

// GetBlueprint returns one blueprint (GET /v1/blueprints/{id}); included
// resources land in Blueprint.Included.
func (c *Client) GetBlueprint(ctx context.Context, id string, o BlueprintGetOptions) (*Blueprint, error) {
	if err := requireID("blueprint id", id); err != nil {
		return nil, err
	}
	q := o.query(TypeBlueprints)
	if err := o.apply(q); err != nil {
		return nil, err
	}
	var doc single[Blueprint]
	if err := c.do(ctx, request{method: http.MethodGet, path: "/v1/blueprints/" + url.PathEscape(id), query: q}, &doc); err != nil {
		return nil, err
	}
	doc.Data.Included = doc.Included
	return &doc.Data, nil
}

// CreateBlueprint creates a blueprint (POST /v1/blueprints, 201).
func (c *Client) CreateBlueprint(ctx context.Context, attrs BlueprintCreateAttributes, rels BlueprintRequestRelationships, opts ...RequestOption) (*Blueprint, error) {
	if attrs.Name == "" {
		return nil, fmt.Errorf("%w: name is required", ErrArgument)
	}
	body := BlueprintCreateRequest{Data: BlueprintCreateData{Type: TypeBlueprints, Attributes: attrs, Relationships: rels}}
	var doc single[Blueprint]
	if err := c.do(ctx, request{method: http.MethodPost, path: "/v1/blueprints", body: body, opts: options(opts)}, &doc); err != nil {
		return nil, err
	}
	return &doc.Data, nil
}

// UpdateBlueprint updates a blueprint (PATCH /v1/blueprints/{id}); a nil
// attrs leaves the attributes alone.
func (c *Client) UpdateBlueprint(ctx context.Context, id string, attrs *BlueprintUpdateAttributes, rels BlueprintRequestRelationships) (*Blueprint, error) {
	if err := requireID("blueprint id", id); err != nil {
		return nil, err
	}
	if attrs == nil && len(rels) == 0 {
		return nil, fmt.Errorf("%w: attributes or relationships are required", ErrArgument)
	}
	body := BlueprintUpdateRequest{Data: BlueprintUpdateData{Type: TypeBlueprints, ID: id, Attributes: attrs, Relationships: rels}}
	var doc single[Blueprint]
	if err := c.do(ctx, request{method: http.MethodPatch, path: "/v1/blueprints/" + url.PathEscape(id), body: body}, &doc); err != nil {
		return nil, err
	}
	return &doc.Data, nil
}

// DeleteBlueprint deletes a blueprint (DELETE /v1/blueprints/{id}, 204).
func (c *Client) DeleteBlueprint(ctx context.Context, id string) error {
	if err := requireID("blueprint id", id); err != nil {
		return err
	}
	return c.do(ctx, request{method: http.MethodDelete, path: "/v1/blueprints/" + url.PathEscape(id)}, nil)
}

// relationshipType maps a blueprint relationship to the type of its
// linkages.
func relationshipType(rel BlueprintRelationship) (string, error) {
	switch rel {
	case BlueprintApps:
		return TypeApps, nil
	case BlueprintConfigurations:
		return TypeConfigurations, nil
	case BlueprintPackages:
		return TypePackages, nil
	case BlueprintOrgDevices:
		return TypeOrgDevices, nil
	case BlueprintUsers:
		return TypeUsers, nil
	case BlueprintUserGroups:
		return TypeUserGroups, nil
	}
	return "", fmt.Errorf("%w: unknown blueprint relationship %q", ErrArgument, rel)
}

// ListBlueprintRelationship lists the ids linked to a blueprint through
// rel (GET /v1/blueprints/{id}/relationships/{rel}).
func (c *Client) ListBlueprintRelationship(ctx context.Context, id string, rel BlueprintRelationship, o ListOptions) (Page[Linkage], error) {
	if err := requireID("blueprint id", id); err != nil {
		return Page[Linkage]{}, err
	}
	typ, err := relationshipType(rel)
	if err != nil {
		return Page[Linkage]{}, err
	}
	q, err := o.query(typ)
	if err != nil {
		return Page[Linkage]{}, err
	}
	return list[Linkage](ctx, c, "/v1/blueprints/"+url.PathEscape(id)+"/relationships/"+string(rel), q)
}

// AddToBlueprint links ids to a blueprint through rel (POST
// /v1/blueprints/{id}/relationships/{rel}, 204).
func (c *Client) AddToBlueprint(ctx context.Context, id string, rel BlueprintRelationship, ids []string, opts ...RequestOption) error {
	return c.linkBlueprint(ctx, http.MethodPost, id, rel, ids, options(opts))
}

// RemoveFromBlueprint unlinks ids from a blueprint through rel (DELETE
// /v1/blueprints/{id}/relationships/{rel}, 204).
func (c *Client) RemoveFromBlueprint(ctx context.Context, id string, rel BlueprintRelationship, ids []string) error {
	return c.linkBlueprint(ctx, http.MethodDelete, id, rel, ids, requestOptions{})
}

func (c *Client) linkBlueprint(ctx context.Context, method, id string, rel BlueprintRelationship, ids []string, o requestOptions) error {
	if err := requireID("blueprint id", id); err != nil {
		return err
	}
	typ, err := relationshipType(rel)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return fmt.Errorf("%w: at least one id is required", ErrArgument)
	}
	body := LinkagesRequest{Data: make([]Linkage, len(ids))}
	for i, v := range ids {
		body.Data[i] = Linkage{Type: typ, ID: v}
	}
	return c.do(ctx, request{method: method, path: "/v1/blueprints/" + url.PathEscape(id) + "/relationships/" + string(rel), body: body, opts: o}, nil)
}

// AuditEventQuery are the parameters of ListAuditEvents. Start and End are
// required by Apple and checked locally.
type AuditEventQuery struct {
	Start, End time.Time
	// ActorID, SubjectID, and Type each accept one value.
	ActorID   string
	SubjectID string
	Type      AuditEventType
	Fields    []string
	Limit     int
	Cursor    string
}

// ListAuditEvents lists audit events in a time range (GET /v1/auditEvents).
func (c *Client) ListAuditEvents(ctx context.Context, o AuditEventQuery) (Page[AuditEvent], error) {
	if o.Start.IsZero() || o.End.IsZero() {
		return Page[AuditEvent]{}, fmt.Errorf("%w: filter[startTimestamp] and filter[endTimestamp] are required", ErrArgument)
	}
	if o.End.Before(o.Start) {
		return Page[AuditEvent]{}, fmt.Errorf("%w: end timestamp precedes start", ErrArgument)
	}
	q, err := ListOptions{Fields: o.Fields, Limit: o.Limit, Cursor: o.Cursor}.query(TypeAuditEvents)
	if err != nil {
		return Page[AuditEvent]{}, err
	}
	q.Set("filter[startTimestamp]", o.Start.UTC().Format(time.RFC3339))
	q.Set("filter[endTimestamp]", o.End.UTC().Format(time.RFC3339))
	if o.ActorID != "" {
		q.Set("filter[actorId]", o.ActorID)
	}
	if o.SubjectID != "" {
		q.Set("filter[subjectId]", o.SubjectID)
	}
	if o.Type != "" {
		q.Set("filter[type]", string(o.Type))
	}
	return list[AuditEvent](ctx, c, "/v1/auditEvents", q)
}

// options folds RequestOptions.
func options(opts []RequestOption) requestOptions {
	var o requestOptions
	for _, fn := range opts {
		fn(&o)
	}
	return o
}
