package dmctl

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"net/url"
)

func runEnrollmentStatus(ctx context.Context, e *env, args []string) error {
	if len(args) == 0 || (args[0] != "values" && args[0] != "errors" && args[0] != "reports") {
		return fmt.Errorf("%w: enrollments status needs values, errors or reports", ErrUsage)
	}
	kind := args[0]
	fs := e.verbFlags("enrollments status " + kind)
	parent := fs.String("parent", "", "parent device enrollment for a user channel")
	cursor := fs.String("cursor", "", "opaque continuation cursor")
	prefix := fs.String("prefix", "", "status path prefix (values only)")
	rest, err := e.parseVerb(fs, args[1:])
	if err != nil {
		return err
	}
	if len(rest) != 2 || (*prefix != "" && kind != "values") {
		return fmt.Errorf(
			"%w: enrollments status %s needs a channel and id; prefix applies only to values",
			ErrUsage,
			kind,
		)
	}
	q := url.Values{}
	if *cursor != "" {
		q.Set("cursor", *cursor)
	}
	if *prefix != "" {
		q.Set("prefix", *prefix)
	}
	path, q := enrollmentPath(rest[0], rest[1], *parent, q)
	c, err := e.client()
	if err != nil {
		return err
	}
	header := []string{"PATH", "VALUE", "FIRST SEEN", "LAST SEEN"}
	keys := []string{"Path", "Value", "FirstSeen", "LastSeen"}
	switch kind {
	case "errors":
		header, keys = []string{
			"SEQ",
			"STATUS ITEM",
			"RECEIVED",
		}, []string{
			"Seq",
			"StatusItem",
			"ReceivedAt",
		}
	case "reports":
		header, keys = []string{
			"SEQ",
			"FULL REPORT",
			"RECEIVED",
		}, []string{
			"Seq",
			"FullReport",
			"ReceivedAt",
		}
	}
	err = e.list(ctx, c, path+"/status/"+kind, q, header, func(item jsontext.Value) []string {
		row := make([]string, len(keys))
		for i, key := range keys {
			row[i] = field(item, key)
			if key == "Value" {
				var value struct{ Value []byte }
				if json.Unmarshal(item, &value) == nil {
					row[i] = string(value.Value)
				}
			}
		}
		return row
	})
	return e.explainNotFound(ctx, c, "ddm", err)
}
