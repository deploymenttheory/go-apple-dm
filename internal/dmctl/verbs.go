package dmctl

import (
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/internal/dmctl/explain"
)

// runExplain answers offline, from the compiled-in schema tables. It builds
// no client and reads neither -server nor -token.
func runExplain(_ context.Context, e *env, args []string) error {
	fs := e.verbFlags("explain")
	var (
		family = fs.String("family", "", "restrict to one schema family")
		target = fs.String("target", "", "grade against a target, e.g. macos:15.0,channel=device,supervised")
		list   = fs.Bool("list", false, "list families, or ids when a family is given")
		paths  = fs.Bool("paths", false, "with -list and -family, list key paths instead of ids")
		first  = fs.Bool("first", false, "print only the first match of an ambiguous identifier")
	)
	rest, err := e.parseVerb(fs, args)
	if err != nil || rest == nil && len(args) > 0 && args[0] == "-h" {
		return err
	}

	if *list {
		return e.explainList(*family, *paths)
	}
	if len(rest) == 0 {
		return fmt.Errorf("%w: explain needs an identifier (try -list)", ErrUsage)
	}

	tgt, err := explain.ParseTarget(*target)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUsage, err)
	}

	matches, err := explain.Resolve(rest[0], *family)
	if err != nil {
		if suggestions := explain.Suggest(rest[0], *family, 10); len(suggestions) > 0 {
			fmt.Fprintf(e.stderr, "dmctl: %v\ndid you mean:\n", err)
			for _, s := range suggestions {
				fmt.Fprintf(e.stderr, "  %s\n", s)
			}
		}
		return fmt.Errorf("%w: %w", ErrUsage, err)
	}
	if *first && len(matches) > 1 {
		matches = matches[:1]
	}
	for i, m := range matches {
		if i > 0 {
			fmt.Fprintln(e.stdout)
		}
		if err := explain.Render(e.stdout, m, tgt); err != nil {
			return fmt.Errorf("dmctl: explain: %w", err)
		}
	}
	return nil
}

func (e *env) explainList(family string, paths bool) error {
	if family == "" {
		for _, f := range explain.Families() {
			fmt.Fprintln(e.stdout, f)
		}
		return nil
	}
	var (
		out []string
		err error
	)
	if paths {
		out, err = explain.Paths(family)
	} else {
		out, err = explain.IDs(family)
	}
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUsage, err)
	}
	for _, s := range out {
		fmt.Fprintln(e.stdout, s)
	}
	return nil
}

// runStatus reports what the server is, which is also how a 404 becomes an
// explanation rather than a mystery.
func runStatus(ctx context.Context, e *env, args []string) error {
	fs := e.verbFlags("status")
	if _, err := e.parseVerb(fs, args); err != nil {
		return err
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	resp, err := c.Do(ctx, http.MethodGet, "/config", nil, nil)
	if err != nil {
		return err
	}
	return e.emit(resp, func(w *tabwriter.Writer) {
		var cfg struct {
			Role, Version string
			Families      []string
			Policy        bool
			BreakGlass    bool
		}
		if err := json.Unmarshal(resp.Body, &cfg); err != nil {
			fmt.Fprintln(w, string(resp.Body))
			return
		}
		fmt.Fprintf(w, "Role:\t%s\n", cfg.Role)
		fmt.Fprintf(w, "Version:\t%s\n", cfg.Version)
		fmt.Fprintf(w, "Families:\t%s\n", strings.Join(cfg.Families, ", "))
		fmt.Fprintf(w, "Authorization:\t%s\n", policyMode(cfg.Policy))
		fmt.Fprintf(w, "Break-glass:\t%s\n", breakGlassMode(cfg.Policy, cfg.BreakGlass))
	})
}

func policyMode(policy bool) string {
	if policy {
		return "policy (principals and Cedar policies)"
	}
	return "static token (development)"
}

// breakGlassMode says whether the server still accepts the static token. It
// is worth a line of its own because the credential is root, bypasses policy,
// has no expiry, and cannot be revoked without a restart. Once principals
// exist it should be gone, so the wording tells the operator what to do
// rather than only reporting a flag.
func breakGlassMode(policy, breakGlass bool) string {
	switch {
	case !breakGlass:
		return "not configured"
	case policy:
		return "ACTIVE, bypasses policy: unset DM_ADMIN_TOKEN once principals exist"
	default:
		return "active (the only credential; no principal store configured)"
	}
}

func runRoutes(ctx context.Context, e *env, args []string) error {
	fs := e.verbFlags("routes")
	if _, err := e.parseVerb(fs, args); err != nil {
		return err
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	resp, err := c.Do(ctx, http.MethodGet, "/routes", nil, nil)
	if err != nil {
		return err
	}
	return e.emit(resp, func(w *tabwriter.Writer) {
		var body struct {
			Routes []struct{ Method, Pattern, Action, Family string }
		}
		if err := json.Unmarshal(resp.Body, &body); err != nil {
			fmt.Fprintln(w, string(resp.Body))
			return
		}
		fmt.Fprintln(w, "METHOD\tPATTERN\tACTION\tFAMILY")
		for _, r := range body.Routes {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", dash(r.Method), r.Pattern, r.Action, r.Family)
		}
	})
}

func runActions(ctx context.Context, e *env, args []string) error {
	fs := e.verbFlags("actions")
	if _, err := e.parseVerb(fs, args); err != nil {
		return err
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	resp, err := c.Do(ctx, http.MethodGet, "/actions", nil, nil)
	if err != nil {
		return e.explainNotFound(ctx, c, "principals", err)
	}
	return e.emit(resp, func(w *tabwriter.Writer) {
		var body struct {
			Items []struct{ ID, Help string }
		}
		if err := json.Unmarshal(resp.Body, &body); err != nil {
			fmt.Fprintln(w, string(resp.Body))
			return
		}
		fmt.Fprintln(w, "ACTION\tWHAT GRANTING IT MEANS")
		for _, a := range body.Items {
			fmt.Fprintf(w, "%s\t%s\n", a.ID, a.Help)
		}
	})
}

// runPrincipals administers credentials.
func runPrincipals(ctx context.Context, e *env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: principals needs a subcommand: list, get, create, rotate, revoke, delete, set-roles", ErrUsage)
	}
	sub, rest := args[0], args[1:]
	c, err := e.client()
	if err != nil {
		return err
	}
	switch sub {
	case "list":
		fs := e.verbFlags("principals list")
		if _, err := e.parseVerb(fs, rest); err != nil {
			return err
		}
		err := e.list(ctx, c, "/principals", nil,
			[]string{"NAME", "ROLES", "ROOT", "TOKEN", "EXPIRES"},
			func(item jsontext.Value) []string {
				return []string{
					field(item, "Name"), dash(fields(item, "Roles")),
					field(item, "Root"), dash(field(item, "TokenID")), dash(field(item, "ExpiresAt")),
				}
			})
		return e.explainNotFound(ctx, c, "principals", err)
	case "get":
		name, rest2, err := needName(e, "principals get", rest)
		if err != nil {
			return err
		}
		_ = rest2
		resp, err := c.Do(ctx, http.MethodGet, "/principals/"+url.PathEscape(name), nil, nil)
		if err != nil {
			return err
		}
		return e.emit(resp, nil)
	case "create":
		return e.createPrincipal(ctx, c, rest)
	case "rotate":
		name, _, err := needName(e, "principals rotate", rest)
		if err != nil {
			return err
		}
		resp, err := c.Do(ctx, http.MethodPost, "/principals/"+url.PathEscape(name)+"/rotate", nil, nil)
		if err != nil {
			return err
		}
		return e.emitToken(resp)
	case "revoke":
		name, _, err := needName(e, "principals revoke", rest)
		if err != nil {
			return err
		}
		_, err = c.Do(ctx, http.MethodPost, "/principals/"+url.PathEscape(name)+"/revoke", nil, nil)
		return err
	case "delete":
		name, _, err := needName(e, "principals delete", rest)
		if err != nil {
			return err
		}
		_, err = c.Do(ctx, http.MethodDelete, "/principals/"+url.PathEscape(name), nil, nil)
		return err
	case "set-roles":
		return e.setRoles(ctx, c, rest)
	default:
		return fmt.Errorf("%w: unknown principals subcommand %q", ErrUsage, sub)
	}
}

func (e *env) createPrincipal(ctx context.Context, c clientDoer, args []string) error {
	fs := e.verbFlags("principals create")
	roles := fs.String("roles", "", "comma-separated roles")
	root := fs.Bool("root", false, "may administer principals and policies")
	rest, err := e.parseVerb(fs, args)
	if err != nil {
		return err
	}
	if len(rest) == 0 {
		return fmt.Errorf("%w: principals create needs a name", ErrUsage)
	}
	body := map[string]any{"Name": rest[0], "Root": *root}
	if *roles != "" {
		body["Roles"] = strings.Split(*roles, ",")
	}
	resp, err := c.Do(ctx, http.MethodPost, "/principals", nil, body)
	if err != nil {
		return err
	}
	return e.emitToken(resp)
}

func (e *env) setRoles(ctx context.Context, c clientDoer, args []string) error {
	fs := e.verbFlags("principals set-roles")
	roles := fs.String("roles", "", "comma-separated roles")
	root := fs.Bool("root", false, "may administer principals and policies")
	rest, err := e.parseVerb(fs, args)
	if err != nil {
		return err
	}
	if len(rest) == 0 {
		return fmt.Errorf("%w: principals set-roles needs a name", ErrUsage)
	}
	body := map[string]any{"Root": *root}
	if *roles != "" {
		body["Roles"] = strings.Split(*roles, ",")
	}
	resp, err := c.Do(ctx, http.MethodPatch, "/principals/"+url.PathEscape(rest[0]), nil, body)
	if err != nil {
		return err
	}
	return e.emit(resp, nil)
}

// emitToken prints a newly minted credential. It is the only place a token is
// ever written, and it goes to stdout alone so it can be captured without the
// surrounding prose.
func (e *env) emitToken(resp *adminResponse) error {
	if e.opts.output != outputHuman {
		return e.emit(resp, nil)
	}
	var body struct {
		Principal struct{ Name string }
		Token     string
	}
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		return e.emit(resp, nil)
	}
	fmt.Fprintln(e.stdout, body.Token)
	fmt.Fprintf(e.stderr, "dmctl: token for %q; it is not stored and cannot be shown again\n", body.Principal.Name)
	return nil
}

// runPolicies administers the authorization policies.
func runPolicies(ctx context.Context, e *env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: policies needs a subcommand: list, get, put, delete", ErrUsage)
	}
	sub, rest := args[0], args[1:]
	c, err := e.client()
	if err != nil {
		return err
	}
	switch sub {
	case "list":
		resp, err := c.Do(ctx, http.MethodGet, "/policies", nil, nil)
		if err != nil {
			return e.explainNotFound(ctx, c, "principals", err)
		}
		return e.emit(resp, func(w *tabwriter.Writer) {
			var body struct {
				Items []struct{ Name, Description string }
			}
			if err := json.Unmarshal(resp.Body, &body); err != nil {
				fmt.Fprintln(w, string(resp.Body))
				return
			}
			fmt.Fprintln(w, "NAME\tDESCRIPTION")
			for _, p := range body.Items {
				fmt.Fprintf(w, "%s\t%s\n", p.Name, dash(p.Description))
			}
		})
	case "get":
		name, _, err := needName(e, "policies get", rest)
		if err != nil {
			return err
		}
		resp, err := c.Do(ctx, http.MethodGet, "/policies/"+url.PathEscape(name), nil, nil)
		if err != nil {
			return err
		}
		if e.opts.output == outputHuman {
			var doc struct{ Source string }
			if json.Unmarshal(resp.Body, &doc) == nil {
				// The source is printed exactly as stored, so an operator
				// edits what they wrote.
				fmt.Fprint(e.stdout, doc.Source)
				if !strings.HasSuffix(doc.Source, "\n") {
					fmt.Fprintln(e.stdout)
				}
				return nil
			}
		}
		return e.emit(resp, nil)
	case "put":
		return e.putPolicy(ctx, c, rest)
	case "delete":
		name, _, err := needName(e, "policies delete", rest)
		if err != nil {
			return err
		}
		_, err = c.Do(ctx, http.MethodDelete, "/policies/"+url.PathEscape(name), nil, nil)
		return err
	default:
		return fmt.Errorf("%w: unknown policies subcommand %q", ErrUsage, sub)
	}
}

func (e *env) putPolicy(ctx context.Context, c clientDoer, args []string) error {
	fs := e.verbFlags("policies put")
	file := fs.String("file", "", "read the policy from a file, or - for stdin")
	desc := fs.String("description", "", "operator note")
	rest, err := e.parseVerb(fs, args)
	if err != nil {
		return err
	}
	if len(rest) == 0 {
		return fmt.Errorf("%w: policies put needs a name", ErrUsage)
	}
	src, err := e.readSource(*file)
	if err != nil {
		return err
	}
	resp, err := c.Do(ctx, http.MethodPut, "/policies/"+url.PathEscape(rest[0]), nil,
		map[string]any{"Source": src, "Description": *desc})
	if err != nil {
		return err
	}
	return e.emit(resp, nil)
}

// readSource reads a policy or payload from a file, stdin, or neither.
func (e *env) readSource(file string) (string, error) {
	switch file {
	case "":
		raw, err := io.ReadAll(e.stdin)
		if err != nil {
			return "", fmt.Errorf("dmctl: read stdin: %w", err)
		}
		return string(raw), nil
	case "-":
		raw, err := io.ReadAll(e.stdin)
		if err != nil {
			return "", fmt.Errorf("dmctl: read stdin: %w", err)
		}
		return string(raw), nil
	default:
		raw, err := os.ReadFile(file) // #nosec G304 -- an operator's own file
		if err != nil {
			return "", fmt.Errorf("dmctl: read %s: %w", file, err)
		}
		return string(raw), nil
	}
}

// runDeclarations manages declarations.
func runDeclarations(ctx context.Context, e *env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: declarations needs a subcommand: get, put, delete", ErrUsage)
	}
	sub, rest := args[0], args[1:]
	c, err := e.client()
	if err != nil {
		return err
	}
	switch sub {
	case "get":
		name, _, err := needName(e, "declarations get", rest)
		if err != nil {
			return err
		}
		resp, err := c.Do(ctx, http.MethodGet, "/declarations/"+url.PathEscape(name), nil, nil)
		if err != nil {
			return e.explainNotFound(ctx, c, "ddm", err)
		}
		return e.emit(resp, nil)
	case "put":
		fs := e.verbFlags("declarations put")
		file := fs.String("file", "", "read the declaration from a file, or - for stdin")
		if _, err := e.parseVerb(fs, rest); err != nil {
			return err
		}
		src, err := e.readSource(*file)
		if err != nil {
			return err
		}
		resp, err := c.Do(ctx, http.MethodPut, "/declarations", nil, src)
		if err != nil {
			return e.explainNotFound(ctx, c, "ddm", err)
		}
		return e.emit(resp, nil)
	case "delete":
		name, _, err := needName(e, "declarations delete", rest)
		if err != nil {
			return err
		}
		_, err = c.Do(ctx, http.MethodDelete, "/declarations/"+url.PathEscape(name), nil, nil)
		return err
	default:
		return fmt.Errorf("%w: unknown declarations subcommand %q", ErrUsage, sub)
	}
}

// needName parses a verb's flags and requires one positional argument.
func needName(e *env, name string, args []string) (string, []string, error) {
	fs := e.verbFlags(name)
	rest, err := e.parseVerb(fs, args)
	if err != nil {
		return "", nil, err
	}
	if len(rest) == 0 {
		return "", nil, fmt.Errorf("%w: %s needs a name", ErrUsage, name)
	}
	return rest[0], rest[1:], nil
}

// runAudit reads the audit trail: who did what, when, and to which
// enrollment. It is the question the trail exists to answer, so the filters
// are the ones an investigation starts from rather than a generic query
// language.
func runAudit(ctx context.Context, e *env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: audit needs a subcommand: list, get", ErrUsage)
	}
	sub, rest := args[0], args[1:]
	c, err := e.client()
	if err != nil {
		return err
	}
	switch sub {
	case "list":
		fs := e.verbFlags("audit list")
		var (
			since      = fs.String("since", "", "only records newer than this age, e.g. 1h or 30m")
			until      = fs.String("until", "", "only records older than this age")
			typ        = fs.String("type", "", "only this event type, e.g. command-queued")
			actor      = fs.String("actor", "", "only this actor, e.g. break-glass")
			enrollment = fs.String("enrollment", "", "only this enrollment id")
		)
		if _, err := e.parseVerb(fs, rest); err != nil {
			return err
		}
		q := url.Values{}
		for key, val := range map[string]string{"type": *typ, "actor": *actor, "enrollment": *enrollment} {
			if val != "" {
				q.Set(key, val)
			}
		}
		// -since takes an age because that is how the question is asked;
		// the wire format is an absolute RFC 3339 instant so the server
		// never has to guess whose clock a relative window belongs to.
		for key, val := range map[string]string{"since": *since, "until": *until} {
			if val == "" {
				continue
			}
			d, err := time.ParseDuration(val)
			if err != nil {
				return fmt.Errorf("%w: -%s %q: %w", ErrUsage, key, val, err)
			}
			q.Set(key, time.Now().Add(-d).UTC().Format(time.RFC3339))
		}
		err := e.list(ctx, c, "/audit", q,
			[]string{"ID", "AT", "TYPE", "ACTOR", "ENROLLMENT", "FIELDS"},
			func(item jsontext.Value) []string {
				return []string{
					field(item, "ID"), field(item, "At"), field(item, "Type"),
					dash(field(item, "Actor")), dash(field(item, "Enrollment")),
					dash(field(item, "Fields")),
				}
			})
		return e.explainNotFound(ctx, c, "audit", err)
	case "get":
		id, _, err := needName(e, "audit get", rest)
		if err != nil {
			return err
		}
		resp, err := c.Do(ctx, http.MethodGet, "/audit/"+url.PathEscape(id), nil, nil)
		if err != nil {
			return e.explainNotFound(ctx, c, "audit", err)
		}
		return e.emit(resp, nil)
	default:
		return fmt.Errorf("%w: unknown audit subcommand %q", ErrUsage, sub)
	}
}
