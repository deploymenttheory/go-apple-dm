package app_test

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm/blueprint"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/utility/appartifact"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/utility/publicappstoreidentity"
	"github.com/deploymenttheory/go-apple-dm/server/blueprints"
)

// These compile-only examples use a configured reference server. The workflow
// test exercises these same helpers against its real admin and device handlers.
// The example HTTP adapter is test code, not an additional public client API.
type appSettingsExample struct {
	client         *http.Client
	baseURL, token string
}

// Example_applicationSettings demonstrates selecting a store application, publishing
// target-validated settings, and assigning the blueprint.
func Example_applicationSettings() {
	ctx := context.Background()
	server := appSettingsExample{
		client:  &http.Client{Timeout: 2 * time.Minute},
		baseURL: "https://mdm.example", token: os.Getenv("DMCTL_TOKEN"),
	}
	var results struct {
		Items []publicappstoreidentity.App `json:"items"`
	}
	search := url.Values{"term": {"Example"}, "country": {"GB"}, "entity": {"iPadSoftware"}}
	if err := server.request(ctx, "GET", "/authoring/app-identities/public-app-store?"+search.Encode(), nil, nil, &results); err != nil {
		fmt.Println(err)
		return
	}
	// Review names, developers and platform metadata. 123 is the listing the
	// author selected; it need not be the first search result.
	payload, err := exampleDenyStoreApp(results.Items, 123)
	if err != nil {
		fmt.Println(err)
		return
	}
	// Supply the intended enrollment's actual target. This example is for an
	// iPad running iOS 27; the server uses schema support for other versions.
	target := url.Values{"os": {"iOS"}, "version": {"27"}, "channel": {"device"}, "supervised": {"true"}}
	record, err := server.publish(ctx, "app-controls", payload, target)
	if err != nil {
		fmt.Println(err)
		return
	}
	// Assignment is a separate, deliberate operation on an existing enrollment.
	if err := server.request(ctx, "PUT", "/enrollments/device/"+url.PathEscape("DEVICE_ID")+"/blueprints/app-controls", nil, nil, nil); err != nil {
		fmt.Println(err)
		return
	}
	// Use this identifier when verifying the device's fetched declaration.
	fmt.Println(record.Compiled.Identifiers["applications"])
}

// Example_applicationSettingsArtifact demonstrates inspecting an artifact, selecting binary
// identities, and publishing and assigning validated controls.
func Example_applicationSettingsArtifact() {
	ctx := context.Background()
	server := appSettingsExample{
		client:  &http.Client{Timeout: 3 * time.Minute},
		baseURL: "https://mdm.example", token: os.Getenv("DMCTL_TOKEN"),
	}
	artifact, err := os.Open("applications.zip")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer func() { _ = artifact.Close() }()
	var report appartifact.Report
	if err := server.request(ctx, "POST", "/authoring/app-identities/artifacts", artifact, http.Header{"Content-Type": {"application/octet-stream"}}, &report); err != nil {
		fmt.Println(err)
		return
	}
	// Review report.SHA256, applications and issues, then copy the chosen
	// candidate's exact Location. It is not an installed filesystem path.
	// The author chooses DeniedBinaries and every observed code-directory hash:
	// this matches this build across architectures, not future app releases.
	payload, err := exampleDenyArtifactBuild(report, "artifact!Example.app")
	if err != nil {
		fmt.Println(err)
		return
	}
	target := url.Values{"os": {"macOS"}, "version": {"27"}, "channel": {"device"}, "supervised": {"true"}}
	record, err := server.publish(ctx, "app-controls", payload, target)
	if err != nil {
		fmt.Println(err)
		return
	}
	// Before assigning macOS binary controls, review Apple's signing-category
	// restriction. Even empty controls reject ad-hoc and unsigned executables.
	if err := server.request(ctx, "PUT", "/enrollments/device/"+url.PathEscape("DEVICE_ID")+"/blueprints/app-controls", nil, nil, nil); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(record.Compiled.Identifiers["applications"])
}

// exampleDenyStoreApp demonstrates an explicit bundle-ID denial. Discovery does
// not decide allow versus deny, or infer app compatibility from the search entity.
func exampleDenyStoreApp(apps []publicappstoreidentity.App, selectedID int64) (*ddm.AppSettings, error) {
	var matches []publicappstoreidentity.App
	for _, candidate := range apps {
		if candidate.ID == selectedID {
			matches = append(matches, candidate)
		}
	}
	if len(matches) != 1 || matches[0].BundleID == "" {
		return nil, fmt.Errorf("select one unambiguous App Store listing with a bundle ID")
	}
	return &ddm.AppSettings{Allowed: &ddm.AppSettingsAllowed{DeniedApps: []string{matches[0].BundleID}}}, nil
}

// exampleDenyArtifactBuild requires a complete report and explicit candidate.
// It selects all observed code directories, including multiple algorithms per
// architecture. It does not derive SigningState or claim native trust validation.
func exampleDenyArtifactBuild(report appartifact.Report, location string) (*ddm.AppSettings, error) {
	if !report.Complete || len(report.Issues) != 0 {
		return nil, fmt.Errorf("review incomplete artifact inspection before authoring")
	}
	var matches []appartifact.Application
	for _, candidate := range report.Applications {
		if candidate.Location == location {
			matches = append(matches, candidate)
		}
	}
	if len(matches) != 1 || len(matches[0].Identity.Architectures) == 0 {
		return nil, fmt.Errorf("select one unambiguous artifact candidate with architectures")
	}
	payload := &ddm.AppSettings{Allowed: &ddm.AppSettingsAllowed{}}
	seen := make(map[string]bool)
	for _, arch := range matches[0].Identity.Architectures {
		if len(arch.CodeDirectories) == 0 {
			return nil, fmt.Errorf("architecture %s has no code-directory hashes", arch.Name)
		}
		for _, directory := range arch.CodeDirectories {
			if directory.CDHash == "" {
				return nil, fmt.Errorf("architecture %s has an empty code-directory hash", arch.Name)
			}
			if !seen[directory.CDHash] {
				payload.Allowed.DeniedBinaries = append(payload.Allowed.DeniedBinaries, ddm.AppSettingsAllowedDeniedBinaries{CDHash: new(directory.CDHash)})
				seen[directory.CDHash] = true
			}
		}
	}
	return payload, nil
}

// publish validates and creates a Blueprint. Updates require the current
// revision in If-Match; a creation conflict is returned to the author unchanged.
func (s appSettingsExample) publish(ctx context.Context, identifier string, payload *ddm.AppSettings, target url.Values) (blueprints.Record, error) {
	var record blueprints.Record
	declaration, err := blueprint.NewDeclaration("applications", payload)
	if err != nil {
		return record, err
	}
	spec := blueprint.Spec{Identifier: identifier, Declarations: []blueprint.Declaration{declaration}}
	raw, err := json.Marshal(spec)
	if err != nil {
		return record, err
	}
	if err := s.request(ctx, "POST", "/blueprints/validate?"+target.Encode(), bytes.NewReader(raw), nil, nil); err != nil {
		return record, err
	}
	err = s.request(ctx, "PUT", "/blueprints/"+url.PathEscape(identifier), bytes.NewReader(raw), nil, &record)
	return record, err
}

// request sends an authenticated admin request and either decodes the successful response or
// drains its body.
func (s appSettingsExample) request(ctx context.Context, method, path string, body io.Reader, headers http.Header, result any) error {
	req, err := http.NewRequestWithContext(ctx, method, s.baseURL+"/admin/v1"+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/json")
	for key, values := range headers {
		req.Header[key] = values
	}
	res, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusNoContent {
		return fmt.Errorf("%s %s: HTTP %d", method, path, res.StatusCode)
	}
	if result == nil {
		_, err = io.Copy(io.Discard, res.Body)
		return err
	}
	return json.UnmarshalRead(io.LimitReader(res.Body, 8<<20), result)
}
