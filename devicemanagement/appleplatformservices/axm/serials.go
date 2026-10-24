package axm

import (
	"context"
	"fmt"
	"strings"
)

// ResolveOrgDevicesBySerial resolves exact, nonempty serials to complete Apple
// organization resources in input order. It enumerates the entire organization
// with bounded pagination; absent or ambiguous serials, duplicate inputs, missing
// resource IDs and incomplete enumeration fail before returning any results.
// IDs are never inferred from serials. This read-only operation uses no inventory
// model and may be used independently of assignment helpers.
func (c *Client) ResolveOrgDevicesBySerial(ctx context.Context, serials []string) ([]OrgDevice, error) {
	if len(serials) == 0 {
		return nil, fmt.Errorf("%w: at least one serial is required", ErrArgument)
	}
	wanted := make(map[string]int, len(serials))
	for i, serial := range serials {
		if strings.TrimSpace(serial) == "" {
			return nil, fmt.Errorf("%w: empty serial", ErrArgument)
		}
		if _, exists := wanted[serial]; exists {
			return nil, fmt.Errorf("%w: duplicate serial", ErrArgument)
		}
		wanted[serial] = i
	}
	first, err := c.ListOrgDevices(ctx, ListOptions{Limit: MaxLimit})
	if err != nil {
		return nil, err
	}
	out := make([]OrgDevice, len(serials))
	found := make(map[string]bool, len(serials))
	resourceIDs := make(map[string]bool, len(serials))
	for page, pageErr := range Pages(ctx, c, first) {
		if pageErr != nil {
			return nil, pageErr
		}
		for _, resource := range page.Items {
			serial := resource.Attributes.SerialNumber
			index, matches := wanted[serial]
			if !matches {
				continue
			}
			if resource.ID == "" || found[serial] || resourceIDs[resource.ID] {
				return nil, fmt.Errorf("%w: ambiguous or unaddressable resource", ErrDeviceSerial)
			}
			found[serial] = true
			resourceIDs[resource.ID] = true
			out[index] = resource
		}
	}
	if len(found) != len(serials) {
		return nil, fmt.Errorf("%w: serial absent from complete enumeration", ErrDeviceSerial)
	}
	return out, nil
}
