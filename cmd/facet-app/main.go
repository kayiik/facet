// Command facet-app is the Facet App: Facet's own window, served as a local
// page in the person's browser, driving a private Compa kernel with the
// Facet Toolkit wired in. It needs no terminal: the person double-clicks it.
//
//	facet-app [--no-browser] [--data DIR]   run the App
//	facet-app hook                          the hook process the kernel starts
//	facet-app version
//
// It finds its parts beside itself, as the App's archive lays them out:
// kernel/compa-kernel and toolkit/bin/facet (FACET_APP_KERNEL and
// FACET_APP_FACET override them for development). Its data lives in a folder
// of its own (FACET_APP_HOME or --data overrides it), so it never touches a
// Compa or a Facet the person installed.
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/kayiik/facet/internal/app/journeys"
	"github.com/kayiik/facet/internal/app/kernel"
	"github.com/kayiik/facet/internal/app/kernel/compa"
	"github.com/kayiik/facet/internal/app/toolkit"
	"github.com/kayiik/facet/internal/app/window"
)

// Version is the App's version; release builds set it with -ldflags.
var Version = "0.0.1-dev"

// agentIdentity is the agent's AGENT.md.
const agentIdentity = `---
name: Facet
description: Facet's video producer in the Facet App.
---
You are Facet, a video producer working with the person in the Facet App.
Use the facet skill and Facet's tools to plan, make and review videos.
Propose consequential work and wait for the person's approval. Paid tools ask
the person in the App before they run; never try to run them another way.
Work inside the workspace, where the person's media and projects are.
`

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "hook":
			if err := compa.RunHookRelay(os.Stdin, os.Stdout); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		case "version", "--version":
			fmt.Println("facet-app", Version)
			return
		}
	}
	flags := flag.NewFlagSet("facet-app", flag.ExitOnError)
	noBrowser := flags.Bool("no-browser", false, "print the window's address instead of opening it")
	dataDir := flags.String("data", "", "the App's data folder (default: a per-user folder; FACET_APP_HOME)")
	flags.Parse(os.Args[1:])
	if err := run(*noBrowser, *dataDir); err != nil {
		fmt.Fprintln(os.Stderr, "facet-app:", err)
		pause()
		os.Exit(1)
	}
}

// pause keeps a double-clicked console window open long enough to read an
// error.
func pause() {
	if runtime.GOOS == "windows" {
		fmt.Fprintln(os.Stderr, "Press Enter to close.")
		fmt.Scanln()
	}
}

func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// decisionKey returns the key that seals the person's review decisions,
// creating it on first use. It lives in the App's data folder, outside the
// agent's workspace (data/compa/workspace), so the agent cannot seal a
// decision record it writes itself. A key that cannot be read as 32 bytes
// is replaced: decisions sealed with it then show as unsealed.
func decisionKey(data string) ([]byte, error) {
	path := filepath.Join(data, "decision.key")
	key, err := os.ReadFile(path)
	if err == nil && len(key) == 32 {
		return key, nil
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read the decision key: %w", err)
	}
	key = make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, fmt.Errorf("write the decision key: %w", err)
	}
	return key, nil
}

func defaultDataDir() (string, error) {
	if dir := os.Getenv("FACET_APP_HOME"); dir != "" {
		return filepath.Abs(dir)
	}
	switch runtime.GOOS {
	case "windows":
		if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
			return filepath.Join(dir, "Facet App"), nil
		}
	case "darwin":
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, "Library", "Application Support", "Facet App"), nil
		}
	default:
		if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
			return filepath.Join(dir, "facet-app"), nil
		}
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, ".local", "share", "facet-app"), nil
		}
	}
	return "", errors.New("cannot find a folder for the App's data; set FACET_APP_HOME")
}

func part(env, relative string) (string, error) {
	path := os.Getenv(env)
	if path == "" {
		exe, err := os.Executable()
		if err != nil {
			return "", err
		}
		path = filepath.Join(filepath.Dir(exe), relative)
	}
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("the App is missing %s (%s); reinstall it", filepath.Base(path), path)
	}
	return filepath.Abs(path)
}

// app hosts the window and owns the kernel.
type app struct {
	opts compa.Options
	win  *window.Server

	mu     sync.Mutex
	kernel *compa.Compa
	ready  bool
}

func (a *app) Kernel() kernel.Kernel {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.kernel == nil || !a.ready {
		return nil
	}
	return a.kernel
}

func (a *app) Model() string { return compa.ConfiguredModel(a.opts.Home) }

// start starts a kernel with opts and relays its events to the window.
func (a *app) start(ctx context.Context, opts compa.Options) error {
	k := compa.New(opts)
	go func() {
		for ev := range k.Events() {
			a.win.Publish(ev)
		}
	}()
	a.mu.Lock()
	a.kernel, a.ready = k, false
	a.mu.Unlock()
	if err := k.Start(ctx); err != nil {
		return err
	}
	a.mu.Lock()
	a.ready = a.kernel == k
	a.mu.Unlock()
	return nil
}

func (a *app) stop(ctx context.Context) {
	a.mu.Lock()
	k := a.kernel
	a.kernel, a.ready = nil, false
	a.mu.Unlock()
	if k != nil {
		k.Stop(ctx)
	}
}

// SetModel saves the model and restarts the kernel with it.
func (a *app) SetModel(ctx context.Context, m window.Model) error {
	a.stop(ctx)
	a.mu.Lock()
	opts := a.opts
	opts.Model = &compa.Model{Endpoint: m.Endpoint, APIKey: m.APIKey, ID: m.ID}
	a.mu.Unlock()
	return a.start(ctx, opts)
}

func run(noBrowser bool, data string) error {
	kernelPath, err := part("FACET_APP_KERNEL", filepath.Join("kernel", exeName("compa-kernel")))
	if err != nil {
		return err
	}
	facetPath, err := part("FACET_APP_FACET", filepath.Join("toolkit", "bin", exeName("facet")))
	if err != nil {
		return err
	}
	if data == "" {
		if data, err = defaultDataDir(); err != nil {
			return err
		}
	}
	if data, err = filepath.Abs(data); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(data, "logs"), 0o700); err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	tk := toolkit.New(facetPath)
	journeys.Toolkit = tk
	key, err := decisionKey(data)
	if err != nil {
		return err
	}
	journeys.SetDecisionKey(key)
	paid, err := tk.PaidTools(ctx)
	if err != nil {
		return fmt.Errorf("the App's Facet Toolkit does not answer: %w", err)
	}
	logFile, err := os.OpenFile(filepath.Join(data, "logs", "kernel.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()

	home := filepath.Join(data, "compa")
	a := &app{opts: compa.Options{
		HomeOptions: compa.HomeOptions{
			Kernel: kernelPath, Facet: facetPath,
			Home: home, FacetHome: filepath.Join(data, "facet"),
			HookCommand: []string{self, "hook"}, Agent: agentIdentity, ApprovalWait: 10 * time.Minute,
		},
		PaidTools: paid, Log: logFile,
	}}
	a.win = window.New(a, tk, randomToken())
	a.win.Projects = projects(data, home)
	if _, err := a.win.Listen(); err != nil {
		return err
	}
	go a.win.Serve(ctx)
	fmt.Println("Facet is running. Its window:", a.win.URL())
	fmt.Println("Keep this window open while you work; close it to stop Facet.")
	if !noBrowser {
		openBrowser(a.win.URL())
	}
	go func() {
		if err := a.start(ctx, a.opts); err != nil {
			fmt.Fprintln(os.Stderr, "facet-app: the agent did not start:", err)
		}
	}()
	<-ctx.Done()
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer stopCancel()
	a.stop(stopCtx)
	return nil
}

// projects locates what the project screens read and write, all in the App's
// data folder. New projects go in the agent's workspace (its projects/
// folder), the only place the agent can read and write, so the agent can work
// on them; folders the person opens elsewhere are kept in the App's catalog.
func projects(data, home string) window.Projects {
	workspace, err := compa.Workspace(home)
	if err != nil {
		// Before the kernel's first start there is no config.json; this is
		// the workspace the kernel will make.
		workspace = filepath.Join(home, "workspace")
	}
	return window.Projects{
		Options: journeys.Options{
			Workspace:       workspace,
			CatalogPath:     filepath.Join(data, "catalog.json"),
			ProductionsRoot: filepath.Join(workspace, "projects"),
		},
		DataDir: data,
	}
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		fmt.Println("Open this address in your browser:", url)
	}
}
