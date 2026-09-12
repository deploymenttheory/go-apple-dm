package sqlcommon

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/crypt"
)

// BlobAAD binds a value to a purpose and unambiguous composite primary key.
func BlobAAD(purpose string, keys ...string) []byte {
	b, _ := json.Marshal(keys)
	return crypt.AAD(purpose, string(b))
}

// SealBlob encrypts a retained value when a keyring is configured.
func SealBlob(k *crypt.Keyring, purpose string, b []byte, keys ...string) ([]byte, error) {
	if len(b) == 0 || k == nil {
		return b, nil
	}
	return k.Seal(b, BlobAAD(purpose, keys...))
}

// OpenBlob authenticates encrypted values and enforces strict plaintext policy.
func OpenBlob(k *crypt.Keyring, purpose string, b []byte, keys ...string) ([]byte, error) {
	if len(b) == 0 {
		return b, nil
	}
	if !crypt.IsSealed(b) {
		if k != nil && k.Strict() {
			return nil, crypt.ErrUnsealed
		}
		return b, nil
	}
	if k == nil {
		return nil, crypt.ErrNoKeyring
	}
	pt, _, err := k.Open(b, BlobAAD(purpose, keys...))
	return pt, err
}

// BlobColumn identifies a retained blob and its ordered primary-key columns.
// Identifiers must be compile-time schema constants, never request input.
type BlobColumn struct {
	Table, Column string
	Keys          []string
}

// RewrapBlobs rotates bounded pages with compare-and-swap writes. Call until zero
// before removing retired keys; concurrent deletions can move page boundaries.
func RewrapBlobs(
	ctx context.Context,
	db *sql.DB,
	d Dialect,
	k *crypt.Keyring,
	cols []BlobColumn,
) (int, error) {
	if k == nil {
		return 0, crypt.ErrNoKeyring
	}
	total := 0
	for _, c := range cols {
		purpose := c.Table + "." + c.Column
		for offset := 0; ; offset += 500 {
			rows, err := db.QueryContext(
				ctx,
				d.Rebind(
					"SELECT "+strings.Join(
						c.Keys,
						",",
					)+","+c.Column+" FROM "+c.Table+" ORDER BY "+strings.Join(
						c.Keys,
						",",
					)+" LIMIT ? OFFSET ?",
				),
				500,
				offset,
			)
			if err != nil {
				return total, err
			}
			type value struct {
				keys []string
				blob []byte
			}
			var page []value
			for rows.Next() {
				v := value{keys: make([]string, len(c.Keys))}
				args := make([]any, len(c.Keys)+1)
				for i := range v.keys {
					args[i] = &v.keys[i]
				}
				args[len(c.Keys)] = &v.blob
				if err = rows.Scan(args...); err != nil {
					break
				}
				page = append(page, v)
			}
			if err == nil {
				err = rows.Err()
			}
			_ = rows.Close()
			if err != nil {
				return total, err
			}
			for _, v := range page {
				if len(v.blob) == 0 {
					continue
				}
				if name, ok := crypt.KeyName(v.blob); ok && name == k.Active() {
					continue
				}
				pt, err := OpenBlob(k, purpose, v.blob, v.keys...)
				if err != nil {
					return total, err
				}
				sealed, err := SealBlob(k, purpose, pt, v.keys...)
				if err != nil {
					return total, err
				}
				where := make([]string, len(c.Keys))
				args := []any{sealed}
				for i, key := range c.Keys {
					where[i] = key + " = ?"
					args = append(args, v.keys[i])
				}
				args = append(args, v.blob)
				res, err := db.ExecContext(
					ctx,
					d.Rebind(
						fmt.Sprintf(
							"UPDATE %s SET %s = ? WHERE %s AND %s = ?",
							c.Table,
							c.Column,
							strings.Join(where, " AND "),
							c.Column,
						),
					),
					args...)
				if err != nil {
					return total, err
				}
				n, err := res.RowsAffected()
				if err != nil {
					return total, err
				}
				total += int(n)
			}
			if len(page) < 500 {
				break
			}
		}
	}
	return total, nil
}
