package publicappstoreidentity_test

import (
	"context"
	json "encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/utility/publicappstoreidentity"
)

type exampleTransport struct{}

// RoundTrip returns the example public App Store lookup response without network access.
func (exampleTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Request: r, Body: io.NopCloser(strings.NewReader(`{"resultCount":1,"results":[{"trackId":123,"bundleId":"com.example.app","trackName":"Example","artistName":"Example Inc","kind":"software","features":["iosUniversal"]}]}`))}, nil
}

// ExampleClient_Search_appSettings demonstrates selecting a public App Store result and validating
// an app allow rule for the target.
func ExampleClient_Search_appSettings() {
	client := publicappstoreidentity.Client{HTTPClient: &http.Client{Transport: exampleTransport{}}}
	apps, err := client.Search(context.Background(), publicappstoreidentity.Query{
		Term: "Example", Developer: "Example Inc",
		Store: publicappstoreidentity.Store{Country: "GB", Entity: publicappstoreidentity.Software},
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	// Resolve ambiguity before authoring policy; never silently take the first
	// result. Here the expected developer has one listing in the fixture.
	if len(apps) != 1 {
		fmt.Println("select the intended app from the search results")
		return
	}
	// Allowing the selected app is the caller's policy decision. The utility
	// supplies its bundle ID without the caller constructing HTTP requests.
	payload := &ddm.AppSettings{Allowed: &ddm.AppSettingsAllowed{
		AllowedApps: []string{apps[0].BundleID},
	}}
	// This example targets a supervised iOS 27 device. Use the actual target's
	// OS, version, channel and enrollment capabilities in an application.
	target := support.Target{
		OS: support.IOS, Version: osversion.New(27, 0, 0),
		Channel: support.ChannelDevice, Supervised: true,
	}
	if err := payload.Validate(target); err != nil {
		fmt.Println(err)
		return
	}
	data, err := json.Marshal(payload)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(string(data))
	// Output: {"Allowed":{"AllowedApps":["com.example.app"]}}
}

// ExampleClient_Search demonstrates public App Store search with an injected fixture transport.
func ExampleClient_Search() {
	// Inject a deterministic response for this executable example. Omit
	// HTTPClient to query Apple's public service with the default transport.
	client := publicappstoreidentity.Client{HTTPClient: &http.Client{Transport: exampleTransport{}}}
	apps, err := client.Search(context.Background(), publicappstoreidentity.Query{
		Term: "Example", Store: publicappstoreidentity.Store{Country: "GB", Entity: publicappstoreidentity.Software},
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, app := range apps {
		fmt.Println(app.ID, app.BundleID, app.Store.Country)
	}
	// Output: 123 com.example.app GB
}
