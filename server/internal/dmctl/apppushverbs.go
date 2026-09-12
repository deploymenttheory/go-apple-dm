package dmctl

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/pushcert"
)

func runAppPush(ctx context.Context, e *env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: apppush needs list, put, or send", ErrUsage)
	}
	fs := e.verbFlags("apppush " + args[0])
	cert := fs.String("cert", "", "certificate PEM/DER file (put)")
	key := fs.String("key", "", "private key PEM file (put)")
	topic := fs.String("topic", "", "app topic")
	environment := fs.String("environment", "", "development or production (send)")
	token := fs.String("token-file", "", "hex token or app registration JSON (send)")
	payload := fs.String("payload-file", "", "JSON payload (send)")
	kind := fs.String("push-type", "alert", "alert or background")
	if err := fs.Parse(reorder(fs, args[1:])); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return wrapError(err)
	}
	c, err := e.client()
	if err != nil {
		return wrapError(err)
	}
	method, path := "GET", "/apppush/credentials"
	var input any
	switch args[0] {
	case "list":
	case "put":
		if *cert == "" || *key == "" {
			return fmt.Errorf("%w: -cert and -key are required", ErrUsage)
		}
		cp, err := e.readSource(*cert)
		if err != nil {
			return wrapError(err)
		}
		kp, err := e.readSource(*key)
		if err != nil {
			return wrapError(err)
		}
		// PEM and DER share the same server validation path.
		normalized, err := pushcert.PEM([]byte(cp))
		if err != nil {
			return wrapError(err)
		}
		method = "PUT"
		input = map[string]string{"Topic": *topic, "CertPEM": string(normalized), "KeyPEM": kp}
	case "send":
		if *topic == "" || *environment == "" || *token == "" || *payload == "" {
			return fmt.Errorf(
				"%w: topic, environment, token-file, and payload-file are required",
				ErrUsage,
			)
		}
		data, err := e.readSource(*token)
		if err != nil {
			return wrapError(err)
		}
		tk, err := readAppToken(data, *topic, *environment)
		if err != nil {
			return wrapError(err)
		}
		p, err := e.readSource(*payload)
		if err != nil {
			return wrapError(err)
		}
		method, path = "POST", "/apppush/send"
		input = map[string]any{
			"Topic":       *topic,
			"Environment": *environment,
			"Token":       hex.EncodeToString(tk),
			"PushType":    *kind,
			"Payload":     json.RawMessage(p),
		}
	default:
		return fmt.Errorf("%w: unknown apppush subcommand", ErrUsage)
	}
	b, err := json.Marshal(input)
	if err != nil {
		return wrapError(err)
	}
	resp, err := c.Do(ctx, method, path, nil, string(b))
	if err != nil {
		return wrapError(err)
	}
	return e.emit(resp, nil)
}
