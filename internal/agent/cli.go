package agent

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mauriceL99/Healthsync-copy/internal/healthdata"
	"github.com/mauriceL99/Healthsync-copy/internal/version"
)

const usage = `healthbridge – Apple Health data for AI agents, end-to-end encrypted.

Usage: healthbridge [--state-dir DIR] <command> [flags]

Setup
  onboard        Create an identity, register it at the relay and show the pairing QR code
  pairing        Show the pairing QR code again
  status         Show pairing, relay and local data status
  unlink         Unlink the paired iPhone (local keys and data are kept)

Data
  fetch          Download, decrypt, validate and store new data from the relay
  summary        Summarize stored data (text, markdown or json)
  series         Daily values of one metric
  workouts       Workouts with detailed heart-rate metrics
  metrics        Supported metrics and how many days have data
  dashboard      Write a self-contained HTML dashboard
  export         Export days as CSV or NDJSON

Garmin (workout files; see docs/GARMIN.md)
  import         Import FIT files, Garmin Connect ZIPs, the account export or a folder
  activities     Imported workouts with pace, power, heart rate, load and training effect
  activity       Full analysis of one workout (laps, zones, bests, strength sets, HYROX)
  training       Weekly volume, training load, acute:chronic ratio, intensity distribution
  records        Personal bests (times, power, estimated 1RM, HYROX)
  delete-activity  Remove imported workouts by ID (--samples: the synthetic samples)

Agents
  mcp            Run as an MCP server on stdio (for Claude Code, Codex, …)
  serve          Run as a service: fetch regularly and answer MCP over HTTP
                 (for ChatGPT and Claude on the web/apps; see docs/SERVER_START.md)
  connector-url  Print the secret connector URL that serve answers on (--upload: the
                 page for uploading Garmin files)

Other
  self-test      Check the executable offline
  dev simulate-device   Pair a simulated iPhone and upload synthetic data
  dev sample-fit        Write synthetic Garmin workouts (run, strength, HYROX) to try import
  version        Print the version

Run "healthbridge <command> --help" for command flags.
State directory: $HEALTHBRIDGE_HOME or ~/.healthbridge (keys and decrypted data; keep it private).
`

// Run executes the CLI and returns the process exit code.
func Run(args []string, env Env) int {
	stateDir := DefaultStateDir()
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		switch {
		case args[0] == "--state-dir" && len(args) > 1:
			stateDir, args = args[1], args[2:]
		case strings.HasPrefix(args[0], "--state-dir="):
			stateDir, args = strings.TrimPrefix(args[0], "--state-dir="), args[1:]
		case args[0] == "-h" || args[0] == "--help":
			fmt.Fprint(env.Out, usage)
			return 0
		case args[0] == "--version":
			fmt.Fprintln(env.Out, version.Version)
			return 0
		default:
			fmt.Fprintf(env.Err, "unknown global flag %s\n\n%s", args[0], usage)
			return 2
		}
	}
	if len(args) == 0 {
		fmt.Fprint(env.Err, usage)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cmd, rest := args[0], args[1:]
	if cmd == "dev" {
		if len(rest) == 0 || (rest[0] != "simulate-device" && rest[0] != "sample-fit") {
			fmt.Fprintln(env.Err, "usage: healthbridge dev simulate-device|sample-fit [flags]")
			return 2
		}
		cmd, rest = "dev "+rest[0], rest[1:]
	}
	err := dispatch(ctx, env, cmd, rest, stateDir)
	switch {
	case err == nil:
		return 0
	case errors.Is(err, flag.ErrHelp):
		return 0
	case errors.Is(err, errUsage):
		return 2
	default:
		fmt.Fprintln(env.Err, "error:", err)
		return 1
	}
}

var errUsage = errors.New("usage error")

type outputFlags struct {
	format string
	out    string
	force  bool
}

func newFlags(env Env, name, synopsis string, stateDir *string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(env.Err)
	fs.StringVar(stateDir, "state-dir", *stateDir, "state directory")
	fs.Usage = func() {
		fmt.Fprintf(env.Err, "Usage: healthbridge %s\n\n", synopsis)
		fs.PrintDefaults()
	}
	return fs
}

func addOutput(fs *flag.FlagSet, o *outputFlags, formats string, def string) {
	fs.StringVar(&o.format, "format", def, "output format: "+formats)
	fs.StringVar(&o.out, "out", "", "write to this file (mode 0600) instead of stdout")
	fs.BoolVar(&o.force, "force", false, "overwrite --out if it exists")
}

func emit(env Env, o outputFlags, data []byte) error {
	if o.out == "" {
		_, err := env.Out.Write(data)
		return err
	}
	if err := writeNewPrivate(o.out, data, o.force); err != nil {
		return err
	}
	abs, _ := filepath.Abs(o.out)
	fmt.Fprintf(env.Err, "Wrote %s (contains sensitive health data; keep it private).\n", abs)
	return nil
}

// parseAnywhere parses flags before, between and after positional arguments
// (the flag package alone stops at the first positional one) and returns the
// positional arguments.
func parseAnywhere(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// emitView writes a view as Markdown text or as JSON.
func emitView(env Env, o outputFlags, v any, text string) error {
	if o.format == "json" {
		data, err := jsonBytes(v)
		if err != nil {
			return err
		}
		return emit(env, o, data)
	}
	return emit(env, o, []byte(text))
}

func jsonBytes(v any) ([]byte, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	return append(data, '\n'), err
}

type windowFlags struct {
	period string
	days   int
	end    string
}

func addWindow(fs *flag.FlagSet, w *windowFlags, defPeriod string, defDays int) {
	if defPeriod != "" {
		fs.StringVar(&w.period, "period", defPeriod, "day, week, month, quarter or year")
	}
	fs.IntVar(&w.days, "days", defDays, "window length in days (overrides --period)")
	fs.StringVar(&w.end, "end", "", "last day of the window, YYYY-MM-DD (default: latest synced day)")
}

// withState opens an initialized state directory for the duration of fn.
func withState(stateDir string, fn func(*State) error) error {
	s, err := OpenState(stateDir)
	if err != nil {
		return err
	}
	defer s.Close()
	if err := s.RequireInit(); err != nil {
		return err
	}
	return fn(s)
}

// withCoverage opens the local database of s for the duration of fn.
func withCoverage(ctx context.Context, s *State, fn func(*DB, Coverage) error) error {
	db, err := OpenDB(s.DBPath())
	if err != nil {
		return err
	}
	defer db.Close()
	cov, err := db.Coverage(ctx)
	if err != nil {
		return err
	}
	return fn(db, cov)
}

func withDB(ctx context.Context, stateDir string, fn func(*State, *DB, Coverage) error) error {
	return withState(stateDir, func(s *State) error {
		return withCoverage(ctx, s, func(db *DB, cov Coverage) error { return fn(s, db, cov) })
	})
}

func dispatch(ctx context.Context, env Env, cmd string, args []string, stateDir string) error {
	switch cmd {
	case "version":
		fmt.Fprintln(env.Out, version.Version)
		return nil
	case "help", "--help", "-h":
		fmt.Fprint(env.Out, usage)
		return nil

	case "onboard":
		var o OnboardOptions
		fs := newFlags(env, cmd, "onboard [--relay URL] [--name NAME] [--rotate] [--new-pairing-code] [--offline]", &stateDir)
		fs.StringVar(&o.Relay, "relay", "", "relay base URL (first onboarding), e.g. https://relay.example.com")
		fs.StringVar(&o.Name, "name", "", "agent name shown in the iPhone app (default: host name)")
		fs.StringVar(&o.RelayToken, "relay-token", "", "registration token if the relay requires one (or HEALTHBRIDGE_RELAY_TOKEN)")
		fs.BoolVar(&o.Rotate, "rotate", false, "archive the current identity and create a new one (the iPhone must pair again)")
		fs.BoolVar(&o.NewPairingCode, "new-pairing-code", false, "invalidate the current QR code and create a new one")
		fs.BoolVar(&o.Offline, "offline", false, "create the identity without contacting the relay")
		noQR := fs.Bool("no-terminal-qr", false, "do not print the QR code in the terminal")
		if err := fs.Parse(args); err != nil {
			return err
		}
		o.QRInTerminal = !*noQR
		s, err := OpenState(stateDir)
		if err != nil {
			return err
		}
		defer s.Close()
		return Onboard(ctx, env, s, o)

	case "pairing":
		fs := newFlags(env, cmd, "pairing [--no-terminal-qr]", &stateDir)
		noQR := fs.Bool("no-terminal-qr", false, "do not print the QR code in the terminal")
		if err := fs.Parse(args); err != nil {
			return err
		}
		s, err := OpenState(stateDir)
		if err != nil {
			return err
		}
		defer s.Close()
		return ShowPairing(env, s, !*noQR)

	case "fetch":
		var o FetchOptions
		fs := newFlags(env, cmd, "fetch [--keep-on-relay] [--json]", &stateDir)
		fs.BoolVar(&o.KeepOnRelay, "keep-on-relay", false, "do not delete fetched envelopes from the relay")
		asJSON := fs.Bool("json", false, "print the result as JSON")
		if err := fs.Parse(args); err != nil {
			return err
		}
		s, err := OpenState(stateDir)
		if err != nil {
			return err
		}
		defer s.Close()
		res, err := Fetch(ctx, env, s, o)
		if err != nil {
			return err
		}
		if *asJSON {
			data, err := jsonBytes(res)
			if err != nil {
				return err
			}
			_, err = env.Out.Write(data)
			return err
		}
		if res.Recovered > 0 {
			fmt.Fprintf(env.Out, "Recovered %d previously unreadable envelopes from the local quarantine.\n", res.Recovered)
		}
		if res.RepairedDays > 0 {
			fmt.Fprintf(env.Err, "warning: %d stored days were unreadable and were replaced with the fetched data.\n", res.RepairedDays)
		}
		if res.Envelopes == 0 {
			fmt.Fprintln(env.Out, "No new data on the relay. Open HealthBridge on the iPhone and tap Sync now, or wait for the next automatic sync.")
			if res.Quarantined > 0 {
				fmt.Fprintf(env.Out, "%d envelopes remain in the local quarantine (see healthbridge status).\n", res.Quarantined)
			}
			return nil
		}
		fmt.Fprintf(env.Out, "Fetched %d envelopes: %d days (%s → %s) and %d workouts updated", res.Envelopes, res.Days, res.FirstDate, res.LastDate, res.Workouts)
		if res.Failed > 0 {
			fmt.Fprintf(env.Out, ", %d unreadable envelopes kept in quarantine", res.Failed)
		}
		if res.Dropped > 0 {
			fmt.Fprintf(env.Out, ", %d invalid values dropped", res.Dropped)
		}
		fmt.Fprintln(env.Out, ".")
		return nil

	case "status":
		fs := newFlags(env, cmd, "status [--offline] [--json]", &stateDir)
		offline := fs.Bool("offline", false, "do not contact the relay")
		asJSON := fs.Bool("json", false, "print JSON")
		if err := fs.Parse(args); err != nil {
			return err
		}
		s, err := OpenState(stateDir)
		if err != nil {
			return err
		}
		defer s.Close()
		st, err := Status(ctx, env, s, *offline)
		if err != nil {
			return err
		}
		if *asJSON {
			data, err := jsonBytes(st)
			if err != nil {
				return err
			}
			_, err = env.Out.Write(data)
			return err
		}
		printStatus(env.Out, st)
		return nil

	case "unlink":
		fs := newFlags(env, cmd, "unlink [--delete-relay-account]", &stateDir)
		deleteAccount := fs.Bool("delete-relay-account", false, "also delete the account and all pending data on the relay")
		if err := fs.Parse(args); err != nil {
			return err
		}
		s, err := OpenState(stateDir)
		if err != nil {
			return err
		}
		defer s.Close()
		return Unlink(ctx, env, s, *deleteAccount)

	case "summary":
		var w windowFlags
		var o outputFlags
		fs := newFlags(env, cmd, "summary [--period week] [--days N] [--end DATE] [--format text|markdown|json] [--out FILE]", &stateDir)
		addWindow(fs, &w, "week", 0)
		addOutput(fs, &o, "text, markdown, json", "text")
		if err := fs.Parse(args); err != nil {
			return err
		}
		return withDB(ctx, stateDir, func(s *State, db *DB, cov Coverage) error {
			win, err := ResolveWindow(w.period, w.days, w.end, cov.LastDate, env.now())
			if err != nil {
				return err
			}
			r, err := BuildReport(ctx, db, win, env.now())
			if err != nil {
				return err
			}
			var data []byte
			switch o.format {
			case "text":
				data = []byte(RenderText(r))
			case "markdown", "md":
				data = []byte(RenderMarkdown(r))
			case "json":
				if data, err = jsonBytes(r); err != nil {
					return err
				}
			default:
				return fmt.Errorf("unknown format %q", o.format)
			}
			return emit(env, o, data)
		})

	case "series":
		var w windowFlags
		var o outputFlags
		fs := newFlags(env, cmd, "series <metric> [--days N] [--end DATE] [--format text|json]", &stateDir)
		addWindow(fs, &w, "", DefaultSeriesDays)
		addOutput(fs, &o, "text, json", "text")
		metricName, flagArgs := splitPositional(args)
		if err := fs.Parse(flagArgs); err != nil {
			return err
		}
		if metricName == "" && fs.NArg() > 0 {
			metricName = fs.Arg(0)
		}
		if metricName == "" {
			fs.Usage()
			return errUsage
		}
		metric, err := ResolveMetric(metricName)
		if err != nil {
			return err
		}
		return withDB(ctx, stateDir, func(s *State, db *DB, cov Coverage) error {
			win, err := ResolveWindow("", w.days, w.end, cov.LastDate, env.now())
			if err != nil {
				return err
			}
			series, err := MetricSeries(ctx, db, metric, win)
			if err != nil {
				return err
			}
			data := []byte(RenderSeries(series))
			if o.format == "json" {
				if data, err = jsonBytes(series); err != nil {
					return err
				}
			}
			return emit(env, o, data)
		})

	case "workouts":
		var w windowFlags
		var o outputFlags
		fs := newFlags(env, cmd, "workouts [--days N] [--end DATE] [--samples] [--format text|json]", &stateDir)
		addWindow(fs, &w, "", DefaultWorkoutDays)
		addOutput(fs, &o, "text, json", "text")
		samples := fs.Bool("samples", false, "include heart-rate samples (json only)")
		if err := fs.Parse(args); err != nil {
			return err
		}
		return withDB(ctx, stateDir, func(s *State, db *DB, cov Coverage) error {
			win, err := ResolveWindow("", w.days, w.end, cov.LastDate, env.now())
			if err != nil {
				return err
			}
			list, err := ListWorkouts(ctx, db, win, *samples)
			if err != nil {
				return err
			}
			data := []byte(RenderWorkouts(list))
			if o.format == "json" {
				if data, err = jsonBytes(list); err != nil {
					return err
				}
			}
			return emit(env, o, data)
		})

	case "import":
		fs := newFlags(env, cmd, "import [--json] FILE_OR_FOLDER...", &stateDir)
		asJSON := fs.Bool("json", false, "print JSON")
		paths, err := parseAnywhere(fs, args)
		if err != nil {
			return err
		}
		if len(paths) == 0 {
			fmt.Fprintln(env.Err, "name at least one .fit file, .zip archive or folder")
			fs.Usage()
			return errUsage
		}
		return withDB(ctx, stateDir, func(s *State, db *DB, _ Coverage) error {
			res, err := Import(ctx, db, s.TmpDir(), paths, env.now())
			if err != nil {
				return err
			}
			if *asJSON {
				data, err := jsonBytes(res)
				if err != nil {
					return err
				}
				_, err = env.Out.Write(data)
				return err
			}
			_, err = io.WriteString(env.Out, RenderImport(res))
			return err
		})

	case "delete-activity":
		fs := newFlags(env, cmd, "delete-activity [--samples] [ID...]", &stateDir)
		samples := fs.Bool("samples", false, "remove all synthetic sample workouts (from dev sample-fit)")
		ids, err := parseAnywhere(fs, args)
		if err != nil {
			return err
		}
		if len(ids) == 0 && !*samples {
			fs.Usage()
			return errUsage
		}
		return withDB(ctx, stateDir, func(_ *State, db *DB, _ Coverage) error {
			n, err := db.DeleteActivities(ctx, ids)
			if err != nil {
				return err
			}
			if *samples {
				k, err := db.DeleteActivitiesWithPrefix(ctx, SampleActivityPrefix)
				if err != nil {
					return err
				}
				n += k
			}
			fmt.Fprintf(env.Out, "Deleted %d activities.\n", n)
			if n < len(ids) {
				fmt.Fprintln(env.Err, "Some IDs were not found; list them with: healthbridge activities --days 3650")
			}
			return nil
		})

	case "activities":
		var w windowFlags
		var o outputFlags
		fs := newFlags(env, cmd, "activities [--days N] [--end DATE] [--kind KIND] [--format text|json]", &stateDir)
		addWindow(fs, &w, "", DefaultActivityDays)
		addOutput(fs, &o, "text, json", "text")
		kind := fs.String("kind", "", "only this kind: "+strings.Join(TrainingKinds, ", "))
		if err := fs.Parse(args); err != nil {
			return err
		}
		return withDB(ctx, stateDir, func(s *State, db *DB, cov Coverage) error {
			win, err := ResolveWindow("", w.days, w.end, cov.LastActivity, env.now())
			if err != nil {
				return err
			}
			list, err := ListActivities(ctx, db, win, *kind)
			if err != nil {
				return err
			}
			return emitView(env, o, list, RenderActivities(list))
		})

	case "activity":
		var o outputFlags
		fs := newFlags(env, cmd, "activity [ID|latest] [--kind KIND] [--samples SECONDS] [--format text|json]", &stateDir)
		addOutput(fs, &o, "text, json", "text")
		kind := fs.String("kind", "", "with latest: the latest activity of this kind")
		every := fs.Int("samples", 0, "include averaged samples every N seconds (json)")
		ids, err := parseAnywhere(fs, args)
		if err != nil {
			return err
		}
		if len(ids) > 1 {
			fs.Usage()
			return errUsage
		}
		return withDB(ctx, stateDir, func(s *State, db *DB, _ Coverage) error {
			d, err := ActivityDetailFor(ctx, db, strings.Join(ids, ""), *kind, *every > 0, *every)
			if err != nil {
				return err
			}
			return emitView(env, o, d, RenderActivityDetail(d))
		})

	case "training":
		var w windowFlags
		var o outputFlags
		fs := newFlags(env, cmd, "training [--days N] [--end DATE] [--format text|json]", &stateDir)
		addWindow(fs, &w, "", DefaultLoadDays)
		addOutput(fs, &o, "text, json", "text")
		if err := fs.Parse(args); err != nil {
			return err
		}
		return withDB(ctx, stateDir, func(s *State, db *DB, cov Coverage) error {
			win, err := ResolveWindow("", w.days, w.end, cov.LastActivity, env.now())
			if err != nil {
				return err
			}
			r, err := BuildTrainingReport(ctx, db, win)
			if err != nil {
				return err
			}
			return emitView(env, o, r, RenderTrainingReport(r))
		})

	case "records":
		var w windowFlags
		var o outputFlags
		fs := newFlags(env, cmd, "records [--days N] [--end DATE] [--format text|json]", &stateDir)
		addWindow(fs, &w, "", DefaultRecordDays)
		addOutput(fs, &o, "text, json", "text")
		if err := fs.Parse(args); err != nil {
			return err
		}
		return withDB(ctx, stateDir, func(s *State, db *DB, cov Coverage) error {
			win, err := ResolveWindow("", w.days, w.end, cov.LastActivity, env.now())
			if err != nil {
				return err
			}
			r, err := ListRecords(ctx, db, win)
			if err != nil {
				return err
			}
			return emitView(env, o, r, RenderRecords(r))
		})

	case "metrics":
		fs := newFlags(env, cmd, "metrics [--json]", &stateDir)
		asJSON := fs.Bool("json", false, "print JSON")
		if err := fs.Parse(args); err != nil {
			return err
		}
		return withDB(ctx, stateDir, func(s *State, db *DB, cov Coverage) error {
			counts, err := db.MetricPaths(ctx)
			if err != nil {
				return err
			}
			if *asJSON {
				var rows []map[string]any
				for _, m := range healthdata.Catalog {
					rows = append(rows, map[string]any{"metric": m.Path(), "label": m.Label, "unit": m.Unit, "aggregation": m.Agg, "healthkit": m.HealthKit, "days_with_data": counts[m.Path()]})
				}
				data, err := jsonBytes(map[string]any{"coverage": cov, "metrics": rows})
				if err != nil {
					return err
				}
				_, err = env.Out.Write(data)
				return err
			}
			fmt.Fprintf(env.Out, "Stored: %d days (%s → %s), %d workouts\n\n", cov.Days, cov.FirstDate, cov.LastDate, cov.Workouts)
			for _, m := range healthdata.Catalog {
				fmt.Fprintf(env.Out, "  %-42s %-30s %-12s %5d days\n", m.Path(), m.Label, m.Unit, counts[m.Path()])
			}
			return nil
		})

	case "dashboard":
		var w windowFlags
		var o outputFlags
		fs := newFlags(env, cmd, "dashboard [--days N] [--end DATE] [--out FILE]", &stateDir)
		addWindow(fs, &w, "", DefaultDashboardDays)
		fs.StringVar(&o.out, "out", "", "output HTML file (default: <state-dir>/dashboard.html)")
		fs.BoolVar(&o.force, "force", true, "overwrite the output file")
		if err := fs.Parse(args); err != nil {
			return err
		}
		return withDB(ctx, stateDir, func(s *State, db *DB, cov Coverage) error {
			win, err := ResolveWindow("", w.days, w.end, cov.LastDate, env.now())
			if err != nil {
				return err
			}
			r, err := BuildReport(ctx, db, win, env.now())
			if err != nil {
				return err
			}
			page, err := Dashboard(ctx, db, win, r)
			if err != nil {
				return err
			}
			if o.out == "" {
				o.out = filepath.Join(s.Dir, "dashboard.html")
			}
			return emit(env, o, page)
		})

	case "export":
		var w windowFlags
		var o outputFlags
		fs := newFlags(env, cmd, "export [--format csv|ndjson] [--days 365] [--out FILE]", &stateDir)
		addWindow(fs, &w, "", 365)
		addOutput(fs, &o, "csv, ndjson", "csv")
		if err := fs.Parse(args); err != nil {
			return err
		}
		return withDB(ctx, stateDir, func(s *State, db *DB, cov Coverage) error {
			win, err := ResolveWindow("", w.days, w.end, cov.LastDate, env.now())
			if err != nil {
				return err
			}
			data, err := Export(ctx, db, win, o.format)
			if err != nil {
				return err
			}
			return emit(env, o, data)
		})

	case "mcp":
		fs := newFlags(env, cmd, "mcp", &stateDir)
		if err := fs.Parse(args); err != nil {
			return err
		}
		m := &MCP{Env: env, StateDir: stateDir}
		return m.Serve(ctx, os.Stdin, env.Out)

	case "serve":
		var o ServeOptions
		fs := newFlags(env, cmd, "serve [--addr :8788] [--fetch-every 15m]", &stateDir)
		fs.StringVar(&o.Addr, "addr", defaultServeAddr, "listen address for MCP over HTTP (put an HTTPS proxy in front)")
		fs.DurationVar(&o.FetchEvery, "fetch-every", defaultFetchInterval, "how often to fetch new data from the relay (0 = never)")
		if err := fs.Parse(args); err != nil {
			return err
		}
		return Serve(ctx, env, stateDir, o)

	case "connector-url":
		fs := newFlags(env, cmd, "connector-url [--public-url https://your-server.example] [--rotate]", &stateDir)
		publicURL := fs.String("public-url", os.Getenv("HEALTHBRIDGE_PUBLIC_URL"), "public HTTPS address of this server (or HEALTHBRIDGE_PUBLIC_URL)")
		rotate := fs.Bool("rotate", false, "create a new secret; the old URL stops working immediately")
		upload := fs.Bool("upload", false, "print the address of the upload page for Garmin files instead")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if *publicURL == "" {
			fmt.Fprintln(env.Err, "--public-url is required, e.g. https://your-server.example")
			fs.Usage()
			return errUsage
		}
		s, err := OpenState(stateDir)
		if err != nil {
			return err
		}
		defer s.Close()
		token, err := s.MCPToken(*rotate)
		if err != nil {
			return err
		}
		u, err := ConnectorURL(*publicURL, token)
		if *upload {
			u, err = UploadURL(*publicURL, token)
		}
		if err != nil {
			return err
		}
		fmt.Fprintln(env.Out, u)
		if *rotate {
			fmt.Fprintln(env.Err, "New secret URL created. Replace the old one in ChatGPT / Claude; it no longer works.")
		}
		return nil

	case "self-test":
		fs := newFlags(env, cmd, "self-test", &stateDir)
		if err := fs.Parse(args); err != nil {
			return err
		}
		return SelfTest(ctx, env)

	case "dev simulate-device":
		var o SimulateOptions
		fs := newFlags(env, "dev simulate-device", "dev simulate-device [--pairing HB1:…] [--days 30] [--timezone Europe/Berlin] [--unlink]", &stateDir)
		fs.StringVar(&o.PairingCode, "pairing", "", "pairing code (default: the one in the state directory)")
		fs.IntVar(&o.Days, "days", 30, "synthetic days to upload")
		fs.StringVar(&o.Timezone, "timezone", "Europe/Berlin", "time zone of the simulated phone")
		fs.Int64Var(&o.Seed, "seed", time.Now().UnixNano(), "random seed")
		fs.BoolVar(&o.Unlink, "unlink", false, "unlink the simulated device after uploading")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if o.PairingCode == "" {
			s, err := OpenState(stateDir)
			if err != nil {
				return err
			}
			if err := s.RequireInit(); err != nil {
				s.Close()
				return err
			}
			o.PairingCode = s.Config.PairingCode
			s.Close()
		}
		fmt.Fprintln(env.Err, "Simulator: uploading SYNTHETIC data (not real health data).")
		return SimulateDevice(ctx, env, o)

	case "dev sample-fit":
		fs := newFlags(env, "dev sample-fit", "dev sample-fit --out FOLDER", &stateDir)
		out := fs.String("out", "", "folder to write the sample files to")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if *out == "" {
			fs.Usage()
			return errUsage
		}
		paths, err := WriteSampleFIT(*out, env.now())
		if err != nil {
			return err
		}
		fmt.Fprintln(env.Err, "Wrote SYNTHETIC workout files (made-up data, not from a real watch). Import them with: healthbridge import "+*out)
		for _, p := range paths {
			fmt.Fprintln(env.Out, p)
		}
		return nil
	}
	fmt.Fprintf(env.Err, "unknown command %q\n\n%s", cmd, usage)
	return errUsage
}

// splitPositional moves a leading positional argument before flags out of args.
func splitPositional(args []string) (string, []string) {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return args[0], args[1:]
	}
	return "", args
}

func printStatus(w io.Writer, st StatusReport) {
	fmt.Fprintf(w, "HealthBridge status\n  State dir:    %s\n  Relay:        %s\n  Agent name:   %s\n  Safety code:  %s\n", st.StateDir, st.Relay, st.AgentName, st.SafetyCode)
	if st.RelayError != "" {
		fmt.Fprintf(w, "  Relay:        unreachable (%s)\n", st.RelayError)
	} else if st.RelayState != nil {
		if d := st.RelayState.Device; d != nil {
			fmt.Fprintf(w, "  iPhone:       %s (%s %s), linked %s, last seen %s\n", firstNonEmpty(d.Name, d.DeviceID), d.Platform, d.AppVersion, d.LinkedAt, d.LastSeenAt)
		} else {
			fmt.Fprintln(w, "  iPhone:       not paired yet (run healthbridge pairing to show the QR code)")
		}
		fmt.Fprintf(w, "  Pending:      %d encrypted uploads waiting on the relay\n", st.RelayState.PendingEnvelopes)
	}
	if lf := st.LastFetch; lf.At != "" {
		fmt.Fprintf(w, "  Last fetch:   %s (%s)", lf.At, lf.Status)
		if lf.Error != "" {
			fmt.Fprintf(w, ": %s", lf.Error)
		}
		fmt.Fprintln(w)
	}
	if st.Local.Days > 0 {
		fmt.Fprintf(w, "  Local data:   %d days (%s → %s), %d workouts\n", st.Local.Days, st.Local.FirstDate, st.Local.LastDate, st.Local.Workouts)
	} else {
		fmt.Fprintln(w, "  Local data:   none yet")
	}
	if st.Local.Activities > 0 {
		fmt.Fprintf(w, "  Garmin:       %d imported activities (%s → %s)\n", st.Local.Activities, st.Local.FirstActivity, st.Local.LastActivity)
	}
	if st.Local.Quarantined > 0 {
		fmt.Fprintf(w, "  Quarantine:   %d unreadable envelopes kept locally, retried on every fetch (update healthbridge if this persists)\n", st.Local.Quarantined)
	}
}
