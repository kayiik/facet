package components

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// stallTimeout ends a download that stops sending; a slow one goes on.
const stallTimeout = 2 * time.Minute

// client fetches downloads; tests point it at a local server.
var client = &http.Client{Transport: &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	TLSHandshakeTimeout:   30 * time.Second,
	ResponseHeaderTimeout: time.Minute,
}}

// fetch places d's file in dir, from opts.From when that folder holds it,
// else from d.URL, and returns its path once its SHA-256 matches the pin.
func fetch(ctx context.Context, d download, dir string, opts Options) (string, error) {
	dest := filepath.Join(dir, d.Name)
	if opts.From != "" {
		local := filepath.Join(opts.From, d.Name)
		if _, err := os.Stat(local); err == nil {
			if err := copyVerified(local, dest, d); err != nil {
				return "", err
			}
			opts.logf("  Using %s from %s", d.Name, opts.From)
			return dest, nil
		}
		if opts.Offline {
			return "", fmt.Errorf("%s is not in %s, and this install is offline", d.Name, opts.From)
		}
	}
	var last error
	for attempt := 1; attempt <= 3; attempt++ {
		if attempt > 1 {
			opts.logf("  Retrying %s (attempt %d of 3): %v", d.Name, attempt, last)
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			}
		}
		last = downloadOnce(ctx, d, dest, opts)
		if last == nil {
			return dest, nil
		}
		var mismatch *checksumError
		if errors.As(last, &mismatch) || ctx.Err() != nil {
			break
		}
	}
	return "", last
}

type checksumError struct{ name, got, want string }

func (e *checksumError) Error() string {
	return fmt.Sprintf("%s does not match its pinned SHA-256 (got %s, want %s); nothing was installed", e.name, e.got, e.want)
}

func downloadOnce(ctx context.Context, d download, dest string, opts Options) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "facet/"+opts.Version)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", d.URL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", d.URL, resp.Status)
	}
	total := d.Size
	if total <= 0 {
		total = resp.ContentLength
	}
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	hash := sha256.New()
	var received int64
	stalled := time.AfterFunc(stallTimeout, func() { cancel(errors.New("the download stopped sending")) })
	defer stalled.Stop()
	progress := newProgress(opts, d.Name, total)
	buf := make([]byte, 256<<10)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			stalled.Reset(stallTimeout)
			if _, err := out.Write(buf[:n]); err != nil {
				out.Close()
				return err
			}
			hash.Write(buf[:n])
			received += int64(n)
			progress.update(received)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			out.Close()
			if cause := context.Cause(ctx); cause != nil && !errors.Is(cause, context.Canceled) {
				return fmt.Errorf("download %s: %w", d.URL, cause)
			}
			return fmt.Errorf("download %s: %w", d.URL, readErr)
		}
	}
	if err := out.Close(); err != nil {
		return err
	}
	if got := hex.EncodeToString(hash.Sum(nil)); !strings.EqualFold(got, d.SHA256) {
		os.Remove(dest)
		return &checksumError{name: d.Name, got: got, want: d.SHA256}
	}
	return nil
}

// copyVerified copies a file the person supplied, checking it like a download.
func copyVerified(src, dest string, d download) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, hash), in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if got := hex.EncodeToString(hash.Sum(nil)); !strings.EqualFold(got, d.SHA256) {
		os.Remove(dest)
		return &checksumError{name: src, got: got, want: d.SHA256}
	}
	return nil
}

// progress prints a download's progress for people, at most every few
// seconds and at each tenth.
type progress struct {
	opts  Options
	name  string
	total int64
	last  time.Time
	tenth int64
}

func newProgress(opts Options, name string, total int64) *progress {
	opts.logf("  Downloading %s%s", name, sizeSuffix(total))
	return &progress{opts: opts, name: name, total: total, last: time.Now()}
}

func (p *progress) update(n int64) {
	if p.total > 0 {
		if tenth := n * 10 / p.total; tenth > p.tenth && tenth < 10 {
			p.tenth = tenth
			p.last = time.Now()
			p.opts.logf("    %d%% (%s of %s)", tenth*10, megabytes(n), megabytes(p.total))
		}
		return
	}
	if time.Since(p.last) > 5*time.Second {
		p.last = time.Now()
		p.opts.logf("    %s", megabytes(n))
	}
}

func sizeSuffix(total int64) string {
	if total <= 0 {
		return ""
	}
	return " (" + megabytes(total) + ")"
}

func megabytes(n int64) string {
	return fmt.Sprintf("%.0f MB", float64(n)/(1<<20))
}
