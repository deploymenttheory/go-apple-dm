package publicappstoreidentity_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/utility/publicappstoreidentity"
)

type exampleTransport struct{}

func (exampleTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Request: r, Body: io.NopCloser(strings.NewReader(`{"resultCount":1,"results":[{"trackId":123,"bundleId":"com.example.app","trackName":"Example"}]}`))}, nil
}

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
