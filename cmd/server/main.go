package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"confighub.local/internal/administration"
	"confighub.local/internal/auth"
	"confighub.local/internal/buildinfo"
	"confighub.local/internal/config"
	"confighub.local/internal/database"
	"confighub.local/internal/httpapi"
	"confighub.local/internal/machineaccess"
	"confighub.local/internal/projects"
	"confighub.local/internal/revisions"
	"confighub.local/internal/server"
	"confighub.local/internal/webui"
)

var (
	errUsage         = errors.New("invalid command usage")
	errConfiguration = errors.New("invalid configuration")
	errRuntime       = errors.New("server runtime failure")
	errBackup        = errors.New("backup failure")
)

const (
	httpReadHeaderTimeout = 5 * time.Second
	httpReadTimeout       = 15 * time.Second
	httpWriteTimeout      = 30 * time.Second
	httpIdleTimeout       = time.Minute
)

type backupOperation func(context.Context, config.Config, string) error

var runBackup backupOperation = func(ctx context.Context, cfg config.Config, output string) error {
	source, err := database.OpenBackupSource(cfg.Database.Path)
	if err != nil {
		return err
	}
	backupErr := database.Backup(ctx, source, output)
	closeErr := source.Close()
	return errors.Join(backupErr, closeErr)
}

func main() {
	os.Exit(runCommandWithIO(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func runCommand(ctx context.Context, args []string, stderr io.Writer) int {
	return runCommandWithIO(ctx, args, io.Discard, stderr)
}

func runCommandWithIO(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	if len(args) == 0 {
		writeUsage(stderr)
		return 2
	}

	var err error
	switch args[0] {
	case "serve":
		var configPath string
		configPath, err = parseServeFlags(args[1:])
		if err == nil {
			err = serve(ctx, configPath, stderr)
		}
	case "backup":
		var configPath, output string
		configPath, output, err = parseBackupFlags(args[1:])
		if err == nil {
			err = backup(ctx, configPath, output)
		}
	case "version":
		if len(args) != 1 {
			err = errUsage
		} else if err := writeBuildVersion(stdout); err != nil {
			fmt.Fprintln(stderr, "confighub-server: version output failed")
			return 1
		}
	default:
		err = errUsage
	}

	switch {
	case err == nil:
		return 0
	case errors.Is(err, errUsage):
		writeUsage(stderr)
		return 2
	case errors.Is(err, errConfiguration):
		fmt.Fprintln(stderr, "confighub-server: invalid configuration")
		return 2
	case errors.Is(err, errBackup):
		fmt.Fprintln(stderr, "confighub-server: backup failed")
		return 1
	default:
		fmt.Fprintln(stderr, "confighub-server: server runtime failure")
		return 1
	}
}

func writeBuildVersion(writer io.Writer) error {
	output := []byte(buildinfo.Version + "\n")
	written, err := writer.Write(output)
	if err != nil {
		return err
	}
	if written != len(output) {
		return io.ErrShortWrite
	}
	return nil
}

func parseServeFlags(args []string) (string, error) {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	configPath := flags.String("config", "", "configuration file")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *configPath == "" {
		return "", errUsage
	}
	return *configPath, nil
}

func parseBackupFlags(args []string) (string, string, error) {
	flags := flag.NewFlagSet("backup", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	configPath := flags.String("config", "", "configuration file")
	output := flags.String("output", "", "backup output file")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *configPath == "" || *output == "" {
		return "", "", errUsage
	}
	return *configPath, *output, nil
}

func serve(parent context.Context, configPath string, stderr io.Writer) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return errConfiguration
	}
	sessionKey, err := loadSessionKey(cfg.Auth.SessionKeyFile)
	if err != nil {
		return errConfiguration
	}

	store, err := database.Open(cfg.Database.Path)
	if err != nil {
		return errRuntime
	}

	syncer := auth.NewUserSyncer(store)
	credentials := auth.NewCredentialService(store)
	sessions := auth.NewSessionManager(store, sessionKey, cfg.Auth.SessionTTL)
	projectService := projects.NewService(store)
	machineService := machineaccess.NewService(store)
	revisionService := revisions.NewService(store, revisions.WithMachineWriteAuthorizer(machineService))
	assets, err := webui.Assets()
	if err != nil {
		_ = store.Close()
		return errRuntime
	}
	logger := slog.New(slog.NewTextHandler(stderr, nil))
	state := server.NewState()
	administrationService := administration.NewService(store, state, buildinfo.Version)
	router, err := httpapi.NewRouter(httpapi.Dependencies{
		Credentials: credentials,
		Sessions:    sessions,
		Projects:    projectService,
		Revisions:   revisionService,
		Machines:    machineService,
		System:      state,
		Admin:       administrationService,
	}, httpapi.Options{
		PublicOrigin:      cfg.Server.PublicURL,
		TrustedProxyCIDRs: cfg.Server.TrustedProxyCIDRs,
		Logger:            logger,
		WebUI:             webui.NewHandler(assets),
	})
	if err != nil {
		_ = store.Close()
		return errConfiguration
	}
	handlerTracker := server.NewHandlerTracker(router)
	httpServer := newHTTPServer(cfg.Server.Listen, handlerTracker, stderr)
	nativeServer := server.New(httpServer,
		server.WithState(state),
		server.WithHandlerDrainer(handlerTracker),
		server.WithLogger(logger),
		server.WithUserReloader(server.UserReloadFunc(func(ctx context.Context) error {
			_, err := syncer.LoadAndSync(ctx, cfg.Auth.UsersFile)
			return err
		})),
	)

	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	reloads := make(chan os.Signal, 1)
	signal.Notify(reloads, syscall.SIGHUP)
	reloadDone := make(chan struct{})
	go func() {
		reloadOnSignals(ctx, reloads, nativeServer.Reload)
		close(reloadDone)
	}()

	runErr, closeErr := runJoinAndClose(
		func() error { return nativeServer.Run(ctx) },
		func() {
			stop()
			signal.Stop(reloads)
		},
		reloadDone,
		store.Close,
	)
	if runErr != nil || closeErr != nil {
		return errRuntime
	}
	return nil
}

func newHTTPServer(address string, handler http.Handler, errorOutput io.Writer) *http.Server {
	return &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: httpReadHeaderTimeout,
		ReadTimeout:       httpReadTimeout,
		WriteTimeout:      httpWriteTimeout,
		IdleTimeout:       httpIdleTimeout,
		MaxHeaderBytes:    1 << 20,
		ErrorLog:          log.New(errorOutput, "http: ", 0),
	}
}

func runJoinAndClose(run func() error, stop func(), joined <-chan struct{}, closeResource func() error) (error, error) {
	runErr := run()
	stop()
	<-joined
	return runErr, closeResource()
}

func reloadOnSignals(ctx context.Context, signals <-chan os.Signal, reload func(context.Context) error) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-signals:
			_ = reload(ctx)
		}
	}
}

func backup(ctx context.Context, configPath, output string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return errConfiguration
	}
	if err := runBackup(ctx, cfg, output); err != nil {
		return errBackup
	}
	return nil
}

func loadSessionKey(path string) ([]byte, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, errConfiguration
	}
	key := bytes.TrimSpace(contents)
	if len(key) < 32 {
		return nil, errConfiguration
	}
	return append([]byte(nil), key...), nil
}

func writeUsage(writer io.Writer) {
	fmt.Fprintln(writer, "Usage:")
	fmt.Fprintln(writer, "  confighub-server serve --config FILE")
	fmt.Fprintln(writer, "  confighub-server backup --config FILE --output FILE")
	fmt.Fprintln(writer, "  confighub-server version")
}
