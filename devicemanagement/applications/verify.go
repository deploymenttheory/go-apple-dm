package applications

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"io"
	"path"
	"time"

	"github.com/deploymenttheory/go-macos-pkg/pkg/flatpkg"
	"github.com/deploymenttheory/go-macos-pkg/pkg/pkgsign"
	"github.com/deploymenttheory/go-macos-pkg/pkg/xar"
)

// VerificationPolicy defaults to Apple's roots and a Developer ID Installer signer.
// Private lab signers require both explicit anchors and AllowPrivateSigner. Revocation,
// notarization and native Gatekeeper assessment are separate checks, never implied here.
type VerificationPolicy struct {
	Anchors            *x509.CertPool
	AllowPrivateSigner bool
	TeamID             string
	RequireTimestamp   bool
	MaxExpandedBytes   int64
}

// Verification records completed checks without treating unavailable checks as passes.
type Verification struct {
	Format            string    `json:"format"`
	ArchiveChecksums  bool      `json:"archiveChecksums"`
	SignatureValid    bool      `json:"signatureValid"`
	Trusted           bool      `json:"trusted"`
	SignerSHA256      string    `json:"signerSHA256"`
	TeamID            string    `json:"teamID,omitempty"`
	DeveloperID       bool      `json:"developerID"`
	TimestampVerified bool      `json:"timestampVerified"`
	VerifiedAt        time.Time `json:"verifiedAt"`
	Revocation        string    `json:"revocation"`
	Notarization      string    `json:"notarization"`
}

// VerifyPackage checks the flat-package structure, signed TOC, every archive entry's
// lengths and checksums, signature integrity and signer trust under the supplied policy.
// It neither extracts package files onto disk nor executes installation scripts.
func VerifyPackage(ctx context.Context, reader io.ReaderAt, size int64, policy VerificationPolicy) (Verification, error) {
	out := Verification{Revocation: "not-checked", Notarization: "not-checked"}
	if policy.AllowPrivateSigner && policy.Anchors == nil {
		return out, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	x, err := xar.Open(reader, size)
	if err != nil {
		return out, fmt.Errorf("%w: package archive: %v", ErrIntegrity, err)
	}
	sig, err := pkgsign.Verify(x, pkgsign.VerifyOptions{Anchors: policy.Anchors, TeamID: policy.TeamID, RequireDeveloperID: !policy.AllowPrivateSigner})
	if err != nil || sig == nil || !sig.Valid() || !sig.Trusted {
		return out, fmt.Errorf("%w: package signature or trust rejected", ErrIntegrity)
	}
	if policy.RequireTimestamp && !sig.TimestampVerified {
		return out, fmt.Errorf("%w: verified timestamp required", ErrIntegrity)
	}
	limit := policy.MaxExpandedBytes
	if limit <= 0 {
		limit = 8 << 30
	}
	var expanded int64
	for _, f := range x.Files() {
		if err = ctx.Err(); err != nil {
			return out, err
		}
		if f.Data != nil {
			if f.Data.Size < 0 || f.Data.Size > limit-expanded {
				return out, ErrTooLarge
			}
			expanded += f.Data.Size
			if f.Data.ArchivedChecksum == nil || f.Data.ArchivedChecksum.Value == "" {
				return out, fmt.Errorf("%w: missing archive checksum", ErrIntegrity)
			}
			if (path.Base(f.Path()) == "PackageInfo" || path.Base(f.Path()) == "Distribution") && f.Data.Size > 4<<20 {
				return out, ErrTooLarge
			}
			r, e := x.OpenVerified(f)
			if e != nil {
				return out, fmt.Errorf("%w: %w", ErrIntegrity, e)
			}
			_, e = io.Copy(io.Discard, contextReader{ctx, r})
			closeErr := r.Close()
			if e != nil {
				return out, fmt.Errorf("%w: %w", ErrIntegrity, e)
			}
			if closeErr != nil {
				return out, closeErr
			}
		}
		for _, ea := range f.EAs {
			if ea.Size < 0 || ea.Size > limit-expanded {
				return out, ErrTooLarge
			}
			expanded += ea.Size
			if ea.ArchivedChecksum == nil || ea.ArchivedChecksum.Value == "" {
				return out, ErrIntegrity
			}
			r, e := x.OpenEAVerified(ea)
			if e != nil {
				return out, e
			}
			_, e = io.Copy(io.Discard, contextReader{ctx, r})
			closeErr := r.Close()
			if e != nil {
				return out, e
			}
			if closeErr != nil {
				return out, closeErr
			}
		}
	}
	p, err := flatpkg.FromXAR(x)
	if err != nil || len(p.Components) == 0 {
		return out, fmt.Errorf("%w: invalid installer structure", ErrIntegrity)
	}
	fingerprint := sha256.Sum256(sig.Signer.Raw)
	out.Format = "flat-pkg"
	out.ArchiveChecksums = true
	out.SignatureValid = true
	out.Trusted = true
	out.SignerSHA256 = hex.EncodeToString(fingerprint[:])
	out.TeamID = sig.TeamID
	out.DeveloperID = sig.DeveloperID
	out.TimestampVerified = sig.TimestampVerified
	out.VerifiedAt = time.Now().UTC()
	return out, nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

// Read checks cancellation between streaming reads.
func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
