package lab

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/profile"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/simulator"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
)

// enrollmentLinkEnroll creates a single-use enrollment link, enrolls a simulator through
// the public landing page and profile download, and requires the redeemed link to be
// refused and listed as redeemed.
func enrollmentLinkEnroll(ctx context.Context, e *Environment, _ string) error {
	d := simulator.New(
		"BENCH-"+randomID(),
		simulator.WithClient(e.Client),
		simulator.WithDDM(map[string]any{}),
	)
	d.SerialNumber = benchSerial
	var created struct{ ID, URL string }
	if err := e.api(ctx, "POST", "/enrollment-links", map[string]string{
		"DeviceID": d.UDID, "Serial": d.SerialNumber, "TTL": "10m",
	}, &created); err != nil {
		return err
	}
	u, err := url.Parse(created.URL)
	if err != nil || !strings.HasPrefix(u.Path, app.PathEnrollmentLinks) {
		return fmt.Errorf("%w: enrollment link URL is not under %s", errOperation, app.PathEnrollmentLinks)
	}
	page, status, err := publicGET(ctx, e, u.Path)
	if err != nil {
		return err
	}
	if status != http.StatusOK || !bytes.Contains(page, []byte(u.Path+"/profile")) {
		return fmt.Errorf("%w: landing page returned HTTP %d without the profile link", errOperation, status)
	}
	b, status, err := publicGET(ctx, e, u.Path+"/profile")
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("%w: profile download returned HTTP %d", errOperation, status)
	}
	if err = d.ApplyProfile(ctx, b, profile.ParseOptions{}); err != nil {
		return wrapError(err)
	}
	if err = d.Enroll(ctx); err != nil {
		return wrapError(err)
	}
	if _, status, err = publicGET(ctx, e, u.Path+"/profile"); err != nil {
		return err
	}
	if status != http.StatusNotFound {
		return fmt.Errorf("%w: redeemed link returned HTTP %d", errOperation, status)
	}
	var links struct{ Items []struct{ ID, State string } }
	if err = e.api(ctx, "GET", "/enrollment-links", nil, &links); err != nil {
		return err
	}
	for _, link := range links.Items {
		if link.ID == created.ID && link.State == app.EnrollmentLinkRedeemed {
			return nil
		}
	}
	return fmt.Errorf("%w: enrollment link is not listed as redeemed", errOperation)
}

// publicGET requests an unauthenticated server path and returns its bounded body and
// status. Only transport and read failures are errors.
func publicGET(ctx context.Context, e *Environment, path string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.URL+path, nil)
	if err != nil {
		return nil, 0, wrapError(err)
	}
	r, err := e.Client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: lab: server request failed", errOperation)
	}
	defer func(body io.Closer) { _ = body.Close() }(r.Body)
	b, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	return b, r.StatusCode, wrapError(err)
}
