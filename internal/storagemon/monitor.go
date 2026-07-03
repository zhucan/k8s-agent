// Package storagemon samples random .snappy files under configured directories,
// measures per-file read latency, and fires a Feishu alert when the average
// latency inside a sliding window crosses the configured threshold.
package storagemon

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"

	larkutil "github.com/k8s-inspect/internal/lark"
)

// Target is one monitored directory + threshold pair.
type Target struct {
	// Dir is the parent directory. Every SampleInterval we pick a random
	// subdirectory of Dir and then a random .snappy file inside it.
	Dir string
	// Threshold is the average read latency (over the sliding window) above
	// which an alert fires.
	Threshold time.Duration
}

// Config is the runtime config for the storage latency monitor.
type Config struct {
	Targets        []Target
	SampleInterval time.Duration // how often to sample (default 5s)
	Window         time.Duration // sliding window duration (default 30m)
	AlertCooldown  time.Duration // suppress repeat alerts (default 30m)
	ChatID         string        // Feishu chat_id to post alerts to
	MentionIDs     []string      // Feishu open_ids to @mention on alert
	LarkClient     *lark.Client
}

// Start launches one sampler goroutine per Target. Non-blocking.
func Start(ctx context.Context, cfg Config) {
	if len(cfg.Targets) == 0 {
		log.Println("[storagemon] no targets configured, skipping")
		return
	}
	if cfg.LarkClient == nil || cfg.ChatID == "" {
		log.Println("[storagemon] LARK client / chat_id missing, skipping")
		return
	}
	if cfg.SampleInterval <= 0 {
		cfg.SampleInterval = 5 * time.Second
	}
	if cfg.Window <= 0 {
		cfg.Window = 30 * time.Minute
	}
	if cfg.AlertCooldown <= 0 {
		cfg.AlertCooldown = 30 * time.Minute
	}

	log.Printf("[storagemon] starting %d target(s), interval=%s window=%s cooldown=%s",
		len(cfg.Targets), cfg.SampleInterval, cfg.Window, cfg.AlertCooldown)
	for _, tgt := range cfg.Targets {
		log.Printf("[storagemon]   • %s (threshold=%s)", tgt.Dir, tgt.Threshold)
		go runSampler(ctx, tgt, cfg)
	}
}

// sample is one latency measurement.
type sample struct {
	At      time.Time
	File    string
	Bytes   int64
	Elapsed time.Duration
	Err     error
}

func runSampler(ctx context.Context, tgt Target, cfg Config) {
	var (
		mu           sync.Mutex
		samples      []sample
		lastAlertAt  time.Time
		inViolation  bool
	)

	ticker := time.NewTicker(cfg.SampleInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("[storagemon] %s: stopped", tgt.Dir)
			return
		case <-ticker.C:
			s := takeSample(ctx, tgt.Dir)

			mu.Lock()
			samples = append(samples, s)
			samples = pruneOld(samples, time.Now().Add(-cfg.Window))
			avg, ok := successAverage(samples)
			winStart := time.Time{}
			if len(samples) > 0 {
				winStart = samples[0].At
			}
			total := len(samples)
			mu.Unlock()

			if s.Err != nil {
				log.Printf("[storagemon] %s: sample err: %v", tgt.Dir, s.Err)
			} else {
				log.Printf("[storagemon] %s: read %s (%d bytes) in %s (avg=%s over %d samples)",
					tgt.Dir, s.File, s.Bytes, s.Elapsed, avg, total)
			}

			if !ok {
				continue
			}
			if avg <= tgt.Threshold {
				mu.Lock()
				inViolation = false
				mu.Unlock()
				continue
			}

			// avg > threshold — decide whether to alert.
			mu.Lock()
			shouldAlert := !inViolation || time.Since(lastAlertAt) >= cfg.AlertCooldown
			if shouldAlert {
				lastAlertAt = time.Now()
				inViolation = true
			}
			snapshot := append([]sample(nil), samples...)
			mu.Unlock()

			if !shouldAlert {
				continue
			}
			go sendAlert(ctx, cfg, tgt, avg, snapshot, winStart)
		}
	}
}

// takeSample picks a random subdirectory of dir, then a random .snappy file
// inside it, reads the file fully, and returns the elapsed time.
func takeSample(ctx context.Context, dir string) sample {
	s := sample{At: time.Now()}

	file, err := pickRandomSnappy(dir)
	if err != nil {
		s.Err = err
		return s
	}
	s.File = file

	start := time.Now()
	n, err := readFile(ctx, file)
	s.Elapsed = time.Since(start)
	s.Bytes = n
	s.Err = err
	return s
}

// pickRandomSnappy chooses a random subdirectory under root, then a random
// .snappy file inside that subdirectory. If the first pick has no .snappy
// files, it tries other subdirectories until one yields a match or all are
// exhausted.
func pickRandomSnappy(root string) (string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", fmt.Errorf("read root %s: %w", root, err)
	}
	subs := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			subs = append(subs, filepath.Join(root, e.Name()))
		}
	}
	if len(subs) == 0 {
		return "", fmt.Errorf("no subdirectories under %s", root)
	}
	shuffle(subs)

	for _, sub := range subs {
		files, err := listSnappyFiles(sub)
		if err != nil {
			log.Printf("[storagemon] list %s: %v", sub, err)
			continue
		}
		if len(files) == 0 {
			continue
		}
		return files[randIntn(len(files))], nil
	}
	return "", fmt.Errorf("no .snappy files found under any subdirectory of %s", root)
}

func listSnappyFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(strings.ToLower(e.Name()), ".snappy") {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out, nil
}

// readFile reads the whole file, returning bytes read and any error. It does
// not decode the snappy stream — the intent is to measure raw filesystem read
// latency, not decompression cost.
func readFile(ctx context.Context, path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	type result struct {
		n   int64
		err error
	}
	done := make(chan result, 1)
	go func() {
		n, err := io.Copy(io.Discard, f)
		done <- result{n, err}
	}()

	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case r := <-done:
		return r.n, r.err
	}
}

func pruneOld(samples []sample, cutoff time.Time) []sample {
	idx := 0
	for idx < len(samples) && samples[idx].At.Before(cutoff) {
		idx++
	}
	if idx == 0 {
		return samples
	}
	return append(samples[:0], samples[idx:]...)
}

func successAverage(samples []sample) (time.Duration, bool) {
	var total time.Duration
	var count int
	for _, s := range samples {
		if s.Err != nil {
			continue
		}
		total += s.Elapsed
		count++
	}
	if count == 0 {
		return 0, false
	}
	return total / time.Duration(count), true
}

func shuffle(xs []string) {
	for i := len(xs) - 1; i > 0; i-- {
		j := randIntn(i + 1)
		xs[i], xs[j] = xs[j], xs[i]
	}
}

// randIntn returns a uniformly random int in [0, n) using crypto/rand so it's
// safe under concurrent use across goroutines (unlike math/rand's default
// source pre-Go1.20). Falls back to a time-based value on read failure.
func randIntn(n int) int {
	if n <= 1 {
		return 0
	}
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return int(time.Now().UnixNano() % int64(n))
	}
	v := binary.BigEndian.Uint64(buf[:])
	return int(v % uint64(n))
}

func sendAlert(ctx context.Context, cfg Config, tgt Target, avg time.Duration, samples []sample, winStart time.Time) {
	content, err := buildAlertCard(tgt, avg, samples, winStart, cfg.MentionIDs)
	if err != nil {
		log.Printf("[storagemon] build alert card: %v", err)
		return
	}
	sendCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	resp, err := cfg.LarkClient.Im.Message.Create(sendCtx, larkim.NewCreateMessageReqBuilder().
		ReceiveIdType("chat_id").
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(cfg.ChatID).
			MsgType(larkim.MsgTypeInteractive).
			Content(content).
			Build()).
		Build())
	if err != nil {
		log.Printf("[storagemon] send alert: %v", err)
		return
	}
	if !resp.Success() {
		log.Printf("[storagemon] send alert: code=%d msg=%s", resp.Code, resp.Msg)
		return
	}
	log.Printf("[storagemon] alert sent: %s avg=%s threshold=%s", tgt.Dir, avg, tgt.Threshold)
}

func buildAlertCard(tgt Target, avg time.Duration, samples []sample, winStart time.Time, mentionIDs []string) (string, error) {
	builder := larkutil.NewCardBuilder()
	builder.SetHeader("Storage Latency Alert — Threshold Exceeded", "red")

	total := len(samples)
	var okCount, errCount int
	var maxElapsed time.Duration
	var maxFile string
	for _, s := range samples {
		if s.Err != nil {
			errCount++
			continue
		}
		okCount++
		if s.Elapsed > maxElapsed {
			maxElapsed = s.Elapsed
			maxFile = s.File
		}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "**Directory:** `%s`\n", tgt.Dir)
	fmt.Fprintf(&sb, "**Threshold:** %s\n", tgt.Threshold)
	fmt.Fprintf(&sb, "**Average (window):** %s ⚠️\n", avg)
	fmt.Fprintf(&sb, "**Slowest sample:** %s\n", maxElapsed)
	if maxFile != "" {
		fmt.Fprintf(&sb, "  ↳ %s\n", maxFile)
	}
	fmt.Fprintf(&sb, "**Samples:** %d (ok=%d, err=%d)\n", total, okCount, errCount)
	if !winStart.IsZero() {
		fmt.Fprintf(&sb, "**Window:** %s → %s\n",
			winStart.Format("2006-01-02 15:04:05"),
			time.Now().Format("2006-01-02 15:04:05"))
	}
	builder.AddMarkdown(sb.String())

	if errCount > 0 {
		builder.AddDivider()
		var errSB strings.Builder
		errSB.WriteString(fmt.Sprintf("**Recent read errors (%d):**\n", errCount))
		shown := 0
		for i := len(samples) - 1; i >= 0 && shown < 3; i-- {
			s := samples[i]
			if s.Err == nil {
				continue
			}
			errSB.WriteString(fmt.Sprintf("• %s — %v\n", s.At.Format("15:04:05"), s.Err))
			shown++
		}
		builder.AddMarkdown(errSB.String())
	}

	if len(mentionIDs) > 0 {
		parts := make([]string, 0, len(mentionIDs))
		for _, id := range mentionIDs {
			parts = append(parts, fmt.Sprintf("<at id=%s></at>", id))
		}
		builder.AddDivider()
		builder.AddMarkdown("**Storage on-call:** " + strings.Join(parts, " ") + " please investigate")
	}

	builder.AddNote(fmt.Sprintf("k8s-agent storage monitor · %s", time.Now().Format("2006-01-02 15:04:05")))
	return builder.Build()
}

// ParseTargets parses a config string of the form
//
//	"/data/a=50ms,/data/b=100ms"
//
// into a []Target. Whitespace around each pair is trimmed.
func ParseTargets(raw string) ([]Target, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	out := make([]Target, 0)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		eq := strings.LastIndex(part, "=")
		if eq < 0 {
			return nil, fmt.Errorf("invalid target %q: expected <dir>=<duration>", part)
		}
		dir := strings.TrimSpace(part[:eq])
		durStr := strings.TrimSpace(part[eq+1:])
		if dir == "" || durStr == "" {
			return nil, fmt.Errorf("invalid target %q: empty dir or duration", part)
		}
		d, err := time.ParseDuration(durStr)
		if err != nil {
			return nil, fmt.Errorf("invalid duration in target %q: %w", part, err)
		}
		if d <= 0 {
			return nil, fmt.Errorf("threshold in %q must be > 0", part)
		}
		out = append(out, Target{Dir: dir, Threshold: d})
	}
	if len(out) == 0 {
		return nil, errors.New("no valid targets parsed")
	}
	return out, nil
}
