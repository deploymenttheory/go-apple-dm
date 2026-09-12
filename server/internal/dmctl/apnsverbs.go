package dmctl

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/push"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/push/apns"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/pushcert"
)

func runAPNS(ctx context.Context, e *env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: apns needs inspect, check, or send", ErrUsage)
	}
	sub := args[0]
	if sub != "inspect" && sub != "check" && sub != "send" {
		return fmt.Errorf("%w: unknown apns command %q", ErrUsage, sub)
	}
	fs := e.verbFlags("apns " + sub)
	certFile := fs.String("cert", "", "PEM chain or DER certificate")
	keyFile := fs.String("key", "", "matching unencrypted PEM private key")
	kind := fs.String("kind", "app", "certificate use: app or mdm (inspect/check)")
	topic := fs.String("topic", "", "topic; defaults to certificate UID")
	environment := fs.String("environment", "", "development or production; required for send")
	tokenFile := fs.String("token-file", "", "hex token or lab registration JSON file")
	payloadFile := fs.String("payload-file", "", "JSON app notification payload")
	pushType := fs.String("push-type", "alert", "alert or background")
	priority := fs.Int("priority", 0, "0 selects the push-type default")
	expiration := fs.Int64(
		"expiration",
		0,
		"APNs expiry in Unix seconds; 0 means immediate delivery only",
	)
	certOut := fs.String("cert-out", "", "write normalized PEM to a new file (inspect/check)")
	if err := fs.Parse(reorder(fs, args[1:])); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("%w: %w", ErrUsage, err)
	}
	rest := fs.Args()
	if len(rest) != 0 || *certFile == "" || (*kind != "app" && *kind != "mdm") {
		return fmt.Errorf(
			"%w: require -cert, kind app or mdm, and no positional arguments",
			ErrUsage,
		)
	}
	data, err := e.readSource(*certFile)
	if err != nil {
		return err
	}
	info, err := pushcert.Inspect([]byte(data))
	if err != nil {
		return fmt.Errorf("dmctl: inspect: %w", err)
	}
	if *topic == "" {
		*topic = info.Topic
	}
	var pair pushcert.Parsed
	if sub != "inspect" {
		if *keyFile == "" {
			return fmt.Errorf(
				"%w: -key is required; a certificate alone cannot authenticate",
				ErrUsage,
			)
		}
		key, err := e.readSource(*keyFile)
		if err != nil {
			return err
		}
		parse := pushcert.ParseApp
		if *kind == "mdm" {
			parse = pushcert.Parse
		}
		pair, err = parse([]byte(data), []byte(key))
		if err != nil {
			return fmt.Errorf("dmctl: credentials: %w", err)
		}
		if err := pushcert.Validate(pair.TLS, *topic, *kind == "mdm", time.Now()); err != nil {
			return fmt.Errorf("dmctl: credentials: %w", err)
		}
	}
	if sub == "send" {
		if *kind != "app" || *certOut != "" || *tokenFile == "" || *payloadFile == "" {
			return fmt.Errorf(
				"%w: send requires app credentials, -token-file and -payload-file; -cert-out is inspection only",
				ErrUsage,
			)
		}
		return e.sendApp(
			ctx,
			pair.TLS,
			*topic,
			*environment,
			*tokenFile,
			*payloadFile,
			*pushType,
			*priority,
			*expiration,
		)
	}
	if *certOut != "" {
		canonical, err := pushcert.PEM([]byte(data))
		if err != nil {
			return fmt.Errorf("dmctl: normalize: %w", err)
		}
		if err := writeNewPrivateFile(*certOut, canonical); err != nil {
			return err
		}
	}
	return e.localJSON(info)
}

func (e *env) sendApp(
	ctx context.Context,
	cert tls.Certificate,
	topic, environment, tokenFile, payloadFile, pushType string,
	priority int,
	expiration int64,
) error {
	host := ""
	switch environment {
	case "development":
		host = apns.DevelopmentHost
	case "production":
		host = apns.ProductionHost
	default:
		return fmt.Errorf("%w: explicitly select -environment development or production", ErrUsage)
	}
	data, err := e.readSource(tokenFile)
	if err != nil {
		return err
	}
	token, err := readAppToken(data, topic, environment)
	if err != nil {
		return err
	}
	payload, err := e.readSource(payloadFile)
	if err != nil {
		return err
	}
	client := apns.NewApp(
		push.StaticCertStore{topic: cert},
		apns.WithHost(host),
		apns.WithTimeout(e.opts.timeout),
	)
	defer client.Close()
	r := client.Send(
		ctx,
		apns.AppRequest{
			Token:      token,
			Topic:      topic,
			Payload:    json.RawMessage(payload),
			PushType:   pushType,
			Priority:   priority,
			Expiration: expiration,
		},
	)
	// Do not serialize error objects or credentials. APNs acceptance is distinct
	// from a receipt recorded by the app.
	if err := e.localJSON(
		map[string]any{
			"outcome":           r.Outcome,
			"status":            r.Status,
			"apnsID":            r.APNSID,
			"reason":            r.Reason,
			"retryAfterSeconds": r.RetryAfter.Seconds(),
		},
	); err != nil {
		return err
	}
	if r.Err != nil {
		return fmt.Errorf("dmctl: APNs send: %w", r.Err)
	}
	return nil
}

func readAppToken(data, topic, environment string) ([]byte, error) {
	text := strings.TrimSpace(data)
	if strings.HasPrefix(text, "{") {
		var registration struct {
			Token       string `json:"token"`
			Topic       string `json:"topic"`
			Environment string `json:"environment"`
		}
		if err := json.Unmarshal([]byte(text), &registration); err != nil {
			return nil, fmt.Errorf("%w: invalid registration JSON: %w", ErrUsage, err)
		}
		if registration.Topic != topic || registration.Environment != environment {
			return nil, fmt.Errorf(
				"%w: registration topic/environment disagrees with the request",
				ErrUsage,
			)
		}
		text = registration.Token
	}
	token, err := hex.DecodeString(text)
	if err != nil || len(token) == 0 {
		return nil, fmt.Errorf("%w: token must be nonempty even-length hexadecimal", ErrUsage)
	}
	return token, nil
}

func (e *env) localJSON(value any) error {
	if err := json.NewEncoder(e.stdout).Encode(value); err != nil {
		return fmt.Errorf("dmctl: write JSON: %w", err)
	}
	return nil
}

// writeNewPrivateFile refuses to truncate existing keys, requests or profiles.
func writeNewPrivateFile(name string, data []byte) error {
	f, err := os.OpenFile(
		name,
		os.O_CREATE|os.O_EXCL|os.O_WRONLY,
		0o600,
	) // #nosec G304 -- explicit operator output path
	if err != nil {
		return fmt.Errorf("dmctl: create %s: %w", name, err)
	}
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil {
		return fmt.Errorf("dmctl: write %s: %w", name, writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("dmctl: close %s: %w", name, closeErr)
	}
	return nil
}

func runPushCSR(e *env, args []string) error {
	fs := e.verbFlags("pushcerts csr")
	keyOut := fs.String("key-out", "", "new PKCS#8 key file; kept on the customer server")
	csrOut := fs.String("csr-out", "", "new PEM CSR file")
	cn := fs.String("cn", "", "CSR common name")
	org := fs.String("organization", "", "CSR organization")
	if err := fs.Parse(reorder(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("%w: %w", ErrUsage, err)
	}
	rest := fs.Args()
	if len(rest) != 0 || *cn == "" || *keyOut == "" || *csrOut == "" || *keyOut == *csrOut {
		return fmt.Errorf("%w: csr requires -cn, -key-out, and a distinct -csr-out", ErrUsage)
	}
	key, csr, err := pushcert.GenerateCSR(pkix.Name{CommonName: *cn, Organization: []string{*org}})
	if err != nil {
		return fmt.Errorf("dmctl: generate CSR: %w", err)
	}
	if err := writeNewPrivateFile(*keyOut, key); err != nil {
		return err
	}
	if err := writeNewPrivateFile(*csrOut, csr); err != nil {
		return fmt.Errorf("%w (key was saved at %s; preserve it)", err, *keyOut)
	}
	return e.localJSON(map[string]string{"keyFile": *keyOut, "csrFile": *csrOut})
}

func runSignCSR(e *env, args []string) error {
	fs := e.verbFlags("pushcerts sign")
	csr := fs.String("csr", "", "customer PEM or DER CSR")
	chain := fs.String("chain", "", "vendor certificate, intermediate(s), root in PEM order")
	key := fs.String("key", "", "vendor RSA signing private key (never the customer's push key)")
	roots := fs.String("roots", "", "trusted Apple root certificate PEM bundle")
	out := fs.String("out", "", "new Apple portal request file")
	if err := fs.Parse(reorder(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("%w: %w", ErrUsage, err)
	}
	rest := fs.Args()
	if len(rest) != 0 || *csr == "" || *chain == "" || *key == "" || *roots == "" || *out == "" {
		return fmt.Errorf("%w: sign requires -csr, -chain, -key, -roots, and -out", ErrUsage)
	}
	data := make([][]byte, 4)
	for j, name := range []string{*csr, *chain, *key, *roots} {
		value, err := e.readSource(name)
		if err != nil {
			return err
		}
		data[j] = []byte(value)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(data[3]) {
		return fmt.Errorf("%w: roots file contains no PEM certificate", ErrUsage)
	}
	envelope, err := pushcert.SignCSR(data[0], data[1], data[2], pool, time.Now())
	if err != nil {
		return fmt.Errorf("dmctl: sign CSR: %w", err)
	}
	if err := writeNewPrivateFile(*out, envelope); err != nil {
		return err
	}
	return e.localJSON(map[string]string{"requestFile": *out})
}

func (e *env) pushCertificateUpload(file, certFile, keyFile, topic string) (string, error) {
	if certFile == "" && keyFile == "" && topic == "" {
		return e.readSource(file)
	}
	if file != "" || certFile == "" || keyFile == "" {
		return "", fmt.Errorf(
			"%w: use either -file JSON or -cert CERT -key KEY [-topic TOPIC]",
			ErrUsage,
		)
	}
	data, err := e.readSource(certFile)
	if err != nil {
		return "", err
	}
	key, err := e.readSource(keyFile)
	if err != nil {
		return "", err
	}
	pair, err := pushcert.Parse([]byte(data), []byte(key))
	if err != nil {
		return "", fmt.Errorf("dmctl: push certificate: %w", err)
	}
	if topic == "" {
		topic = pair.Topic
	}
	if err := pushcert.Validate(pair.TLS, topic, true, time.Now()); err != nil {
		return "", fmt.Errorf("dmctl: push certificate: %w", err)
	}
	canonical, err := pushcert.PEM([]byte(data))
	if err != nil {
		return "", fmt.Errorf("dmctl: normalize: %w", err)
	}
	body, err := json.Marshal(
		map[string]string{"Topic": topic, "CertPEM": string(canonical), "KeyPEM": key},
	)
	if err != nil {
		return "", fmt.Errorf("dmctl: upload JSON: %w", err)
	}
	return string(body), nil
}
