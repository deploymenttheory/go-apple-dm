package mdmctl

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/internal/mdmctl/adminclient"
)

// Exit codes. They are documented and distinct so a script can tell a usage
// mistake from a refusal from a partial success. Both reference CLIs exit 1
// from arbitrary depth, which tells a caller nothing.
const (
	ExitOK       = 0
	ExitFailed   = 1 // the request failed: network, 5xx, malformed response
	ExitUsage    = 2 // unknown verb, bad flag, missing argument
	ExitPartial  = 3 // the request succeeded but some targets did not
	ExitAuth     = 4 // 401 or 403
	exitInternal = 1
)

// Errors that select an exit code.
var (
	// ErrUsage is a command-line mistake rather than a server refusal.
	ErrUsage = errors.New("usage")
	// ErrPartial is a request that succeeded with some targets refused.
	ErrPartial = errors.New("partial success")
)

// env is everything a verb needs: the parsed globals, the streams, and a
// lazily built client.
type env struct {
	opts   options
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	getenv func(string) string
}

// options are the global flags, which are accepted before or after the verb.
type options struct {
	server   string
	token    string
	context  string
	config   string
	output   string
	limit    int
	all      bool
	timeout  time.Duration
	insecure bool
	verbose  bool
}

// DefaultServer is used when neither -server, MDMCTL_SERVER, nor the selected
// config context names one.
const DefaultServer = "http://127.0.0.1:8080"

// Output modes.
const (
	outputHuman  = "human"
	outputJSON   = "json"
	outputNDJSON = "ndjson"
)

// Run parses argv and dispatches. It returns an error; the caller maps it to
// an exit code with ExitCode.
func Run(ctx context.Context, args []string, getenv func(string) string, stdin io.Reader, stdout, stderr io.Writer) error {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	e := &env{stdin: stdin, stdout: stdout, stderr: stderr, getenv: getenv}

	fs := newFlagSet("mdmctl", stderr)
	e.opts.bind(fs, defaultsFromEnv(getenv))
	fs.Usage = func() { usage(stderr, fs) }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("%w: %w", ErrUsage, err)
	}
	rest := fs.Args()
	if len(rest) == 0 {
		usage(stderr, fs)
		return fmt.Errorf("%w: no command", ErrUsage)
	}

	verb, rest := rest[0], rest[1:]
	cmd, ok := commands()[verb]
	if !ok {
		usage(stderr, fs)
		return fmt.Errorf("%w: unknown command %q", ErrUsage, verb)
	}
	if e.opts.insecure {
		fmt.Fprintln(stderr, "mdmctl: warning: -insecure disables TLS verification")
	}
	return cmd.run(ctx, e, rest)
}

// ExitCode maps an error to a process exit status.
func ExitCode(err error) int {
	switch {
	case err == nil:
		return ExitOK
	case errors.Is(err, ErrUsage):
		return ExitUsage
	case errors.Is(err, ErrPartial):
		return ExitPartial
	case errors.Is(err, adminclient.ErrUnauthorized), errors.Is(err, adminclient.ErrForbidden):
		return ExitAuth
	default:
		return ExitFailed
	}
}

// clientDoer is the part of adminclient.Client the verb helpers need, so a
// helper can be exercised without standing up a server.
type clientDoer interface {
	Do(ctx context.Context, method, path string, query url.Values, body any) (*adminclient.Response, error)
}

// adminResponse names the client's response type locally.
type adminResponse = adminclient.Response

// command is one verb.
type command struct {
	name    string
	summary string
	run     func(ctx context.Context, e *env, args []string) error
}

// Verbs lists every registered verb, sorted. It exists so a test can assert
// the help and the dispatch table agree without repeating the list, which is
// what let new verbs ship undiscoverable.
func Verbs() []string {
	out := make([]string, 0, len(commands()))
	for name := range commands() {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// commands is the dispatch table. A map rather than a switch so a test can
// assert every verb has help, and so gocyclo does not see one enormous
// function.
func commands() map[string]command {
	cmds := []command{
		{"explain", "describe a command, declaration, payload, or status item, offline", runExplain},
		{"status", "show the server's role, families, and version", runStatus},
		{"routes", "list the admin routes the server serves", runRoutes},
		{"principals", "administer admin credentials", runPrincipals},
		{"policies", "administer authorization policies", runPolicies},
		{"actions", "list the actions a policy can grant", runActions},
		{"declarations", "manage declarations", runDeclarations},
		{"audit", "read the audit trail", runAudit},
		{"enrollments", "list, read, and disable enrollments", runEnrollments},
		{"commands", "send, read, and clear queued MDM commands", runCommands},
		{"push", "wake a device now without queueing anything", runPush},
		{"pushcerts", "read push certificates and upload a renewal", runPushCerts},
		{"sets", "manage declaration sets and their assignment", runSets},
		{"notify", "drain declaration changes and wake the affected devices", runNotify},
		{"export", "export enrollments for migration", runExport},
		{"import", "import an exported enrollment record", runImport},
		{"api", "call any admin route by method and path", runAPI},
		{"version", "print the mdmctl version", runVersion},
	}
	out := make(map[string]command, len(cmds))
	for _, c := range cmds {
		out[c.name] = c
	}
	return out
}

// defaultsFromEnv is the starting point before any flag is seen.
func defaultsFromEnv(getenv func(string) string) options {
	return options{
		// Left empty when unset so a config context's Server can apply.
		// Defaulting here made e.opts.server never empty, which is what made
		// the context field below unreachable.
		server:  getenv("MDMCTL_SERVER"),
		token:   getenv("MDMCTL_TOKEN"),
		context: getenv("MDMCTL_CONTEXT"),
		config:  getenv("MDMCTL_CONFIG"),
		output:  firstNonEmpty(getenv("MDMCTL_OUTPUT"), outputHuman),
		timeout: adminclient.DefaultTimeout,
	}
}

// bind attaches the global flags, taking their defaults from def.
//
// A verb re-binds the same variables so a global may be written after the
// verb, and def is then the values parsed before it. Seeding from the
// environment again at that point would silently discard everything the
// operator wrote before the verb, which is the bug this signature exists to
// prevent.
func (o *options) bind(fs *flag.FlagSet, def options) {
	fs.StringVar(&o.server, "server", def.server, "server base URL (MDMCTL_SERVER)")
	fs.StringVar(&o.token, "token", def.token, "bearer token, @file, or env:NAME (MDMCTL_TOKEN)")
	fs.StringVar(&o.context, "context", def.context, "context from the config file (MDMCTL_CONTEXT)")
	fs.StringVar(&o.config, "config", def.config, "config file path (MDMCTL_CONFIG)")
	fs.StringVar(&o.output, "output", def.output, "output: human, json, or ndjson")
	fs.IntVar(&o.limit, "limit", def.limit, "page size (0 uses the server default)")
	fs.BoolVar(&o.all, "all", def.all, "follow cursors to the end of a listing")
	fs.DurationVar(&o.timeout, "timeout", def.timeout, "per-request timeout")
	fs.BoolVar(&o.insecure, "insecure", def.insecure, "skip TLS verification (warns on every use)")
	fs.BoolVar(&o.verbose, "v", def.verbose, "trace requests to stderr")
}

// newFlagSet returns a flag set that reports errors to w and never exits.
func newFlagSet(name string, w io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(w)
	return fs
}

// verbFlags returns a flag set for a verb, seeded with the same global flag
// variables so `mdmctl -json declarations list` and `mdmctl declarations list
// -json` behave identically. The flag package stops at the first non-flag
// argument, so without this the second form would not parse.
func (e *env) verbFlags(name string) *flag.FlagSet {
	fs := newFlagSet(name, e.stderr)
	// Copy first: bind writes through pointers into e.opts, so the defaults
	// must be a snapshot of what was parsed before the verb.
	cur := e.opts
	e.opts.bind(fs, cur)
	return fs
}

// reorder moves flag arguments ahead of positional ones.
//
// The flag package stops parsing at the first non-flag argument, so without
// this `mdmctl explain DeviceLock -target macos:15.0` would silently ignore
// the target and print the untargeted table: the worst kind of wrong, since
// it answers a different question without saying so. Everything after "--" is
// positional.
func reorder(fs *flag.FlagSet, args []string) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if len(a) < 2 || a[0] != '-' {
			positional = append(positional, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if strings.ContainsRune(name, '=') {
			continue
		}
		f := fs.Lookup(name)
		if f == nil {
			// Unknown: leave it for Parse to report by name.
			continue
		}
		if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
			continue
		}
		// The flag takes a value, so the next argument belongs with it.
		if i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	// Keep a terminator so Parse stops at the positionals too: without it an
	// argument that merely looks like a flag, such as one after an explicit
	// "--", would be parsed as one after being moved.
	return append(append(flags, "--"), positional...)
}

// parseVerb parses a verb's flags, leaving positional arguments. Flags may
// appear before or after those arguments.
func (e *env) parseVerb(fs *flag.FlagSet, args []string) ([]string, error) {
	if err := fs.Parse(reorder(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, nil
		}
		return nil, fmt.Errorf("%w: %w", ErrUsage, err)
	}
	return fs.Args(), nil
}

// usage prints the verb list and the global flags.
func usage(w io.Writer, fs *flag.FlagSet) {
	fmt.Fprintln(w, "mdmctl administers a go-apple-mdm reference server.")
	fmt.Fprintln(w, "\nUsage:\n  mdmctl [flags] <command> [flags] [arguments]")
	fmt.Fprintln(w, "\nCommands:")
	cmds := commands()
	names := make([]string, 0, len(cmds))
	for n := range cmds {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(w, "  %-14s %s\n", n, cmds[n].summary)
	}
	fmt.Fprintln(w, "\nFlags:")
	fs.PrintDefaults()
	fmt.Fprintln(w, "\nexplain needs no server. Everything else reads -server and -token.")
}

func runVersion(_ context.Context, e *env, _ []string) error {
	fmt.Fprintln(e.stdout, version())
	return nil
}

// firstNonEmpty returns the first non-empty string.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// resolveToken reads the credential. A bare value is used as-is; "@path"
// reads a file; "env:NAME" reads an environment variable. A token is never
// accepted as a positional argument, because argv is world-readable in ps.
func (e *env) resolveToken() (string, error) {
	tok := e.opts.token
	if tok == "" {
		cfg, err := e.loadConfig()
		if err != nil {
			return "", err
		}
		if cfg != nil {
			return cfg.token(e.getenv)
		}
		return "", nil
	}
	return readTokenSpec(tok, e.getenv)
}

func readTokenSpec(spec string, getenv func(string) string) (string, error) {
	switch {
	case strings.HasPrefix(spec, "@"):
		raw, err := os.ReadFile(spec[1:])
		if err != nil {
			return "", fmt.Errorf("mdmctl: read token file: %w", err)
		}
		return strings.TrimSpace(string(raw)), nil
	case strings.HasPrefix(spec, "env:"):
		name := strings.TrimPrefix(spec, "env:")
		v := strings.TrimSpace(getenv(name))
		if v == "" {
			return "", fmt.Errorf("mdmctl: %s is empty", name)
		}
		return v, nil
	default:
		return spec, nil
	}
}

// client builds the admin client for the resolved server and credential.
func (e *env) client() (*adminclient.Client, error) {
	server := e.opts.server
	tok, err := e.resolveToken()
	if err != nil {
		return nil, err
	}
	// Precedence: the -server flag or MDMCTL_SERVER, then the selected config
	// context, then the built-in default.
	if server == "" {
		if cfg, cerr := e.loadConfig(); cerr == nil && cfg != nil {
			server = cfg.Server
		}
	}
	if server == "" {
		server = DefaultServer
	}
	var trace func(string)
	if e.opts.verbose {
		trace = func(s string) { fmt.Fprintln(e.stderr, "mdmctl:", s) }
	}
	c, err := adminclient.New(adminclient.Config{
		BaseURL: server, Token: tok, Timeout: e.opts.timeout,
		Insecure: e.opts.insecure, Trace: trace,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUsage, err)
	}
	return c, nil
}

// explainNotFound turns a 404 into a sentence when the route is missing
// because of the role the server runs rather than because the object is not
// there. No reference server has roles, so none needs this; ours does, and a
// bare 404 sends an operator looking for the wrong thing.
func (e *env) explainNotFound(ctx context.Context, c *adminclient.Client, family string, err error) error {
	if !errors.Is(err, adminclient.ErrNotFound) || family == "" {
		return err
	}
	cfg, cerr := c.ServerConfig(ctx)
	if cerr != nil {
		return err
	}
	for _, f := range cfg.Families {
		if f == family {
			return err
		}
	}
	return fmt.Errorf("%w (this server runs role=%s, which does not serve %s)", err, cfg.Role, family)
}
