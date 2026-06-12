package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

func main() {
	var (
		apply        = flag.Bool("apply", false, "Actually replace files (default: dry-run)")
		maxKiB       = flag.Int("max-kib", 600, "Target maximum size in KiB")
		thresholdKiB = flag.Int("threshold-kib", 600, "Skip files already <= this size")
		limit        = flag.Int("limit", 0, "Process at most N files (0 = no limit)")
		concurrency  = flag.Int("concurrency", 3, "Parallel workers")
		logPath      = flag.String("log", "processed.jsonl", "Progress log file (resumable)")
		dumpDir      = flag.String("dump-dir", "", "If set, write {orig,new} pairs into this directory for visual inspection")
		quotaOnly    = flag.Bool("quota", false, "Just print live Drive storage quota and exit")
	)
	flag.Parse()

	if _, err := exec.LookPath("magick"); err != nil {
		log.Fatal("ImageMagick `magick` introuvable. Installe avec: brew install imagemagick")
	}
	if *dumpDir != "" {
		if err := os.MkdirAll(*dumpDir, 0o755); err != nil {
			log.Fatalf("create dump-dir: %v", err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Fprintln(os.Stderr, "\nArret demande, fin propre du fichier en cours…")
		cancel()
	}()

	httpClient, err := getClient(ctx)
	if err != nil {
		log.Fatalf("auth: %v", err)
	}
	srv, err := drive.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		log.Fatalf("drive service: %v", err)
	}

	if *quotaOnly {
		printQuota(ctx, srv)
		return
	}

	plog, err := openLog(*logPath)
	if err != nil {
		log.Fatalf("open log: %v", err)
	}
	defer plog.Close()

	processed, err := loadProcessed(*logPath)
	if err != nil {
		log.Fatalf("load log: %v", err)
	}
	if len(processed) > 0 {
		fmt.Printf("Reprise: %d fichiers deja traites avec succes seront ignores.\n", len(processed))
	}

	fmt.Println("Listing des images JPEG en cours…")
	files, err := listImages(ctx, srv, int64(*thresholdKiB)*1024, processed, *limit)
	if err != nil {
		log.Fatalf("list: %v", err)
	}

	var totalOrig int64
	for _, f := range files {
		totalOrig += f.Size
	}
	fmt.Printf("Candidats: %d fichiers, total %s\n", len(files), human(totalOrig))
	if !*apply {
		fmt.Println("DRY RUN — passe --apply pour vraiment remplacer.")
	}
	if len(files) == 0 {
		return
	}

	r := &runner{
		srv:     srv,
		plog:    plog,
		maxSize: *maxKiB * 1024,
		apply:   *apply,
		dumpDir: *dumpDir,
	}

	jobs := make(chan imgFile)
	var wg sync.WaitGroup
	start := time.Now()

	for range *concurrency {
		wg.Go(func() {
			for f := range jobs {
				if ctx.Err() != nil {
					return
				}
				r.processOne(ctx, f)
			}
		})
	}

	for _, f := range files {
		select {
		case <-ctx.Done():
		case jobs <- f:
		}
	}
	close(jobs)
	wg.Wait()

	elapsed := time.Since(start)
	bytesIn, bytesOut := r.stats.bytesIn.Load(), r.stats.bytesOut.Load()
	saved := bytesIn - bytesOut
	fmt.Printf("\nTermine en %s. OK=%d  skip=%d  err=%d.\n", elapsed.Round(time.Second), r.stats.done.Load(), r.stats.skipped.Load(), r.stats.errs.Load())
	if *apply {
		fmt.Printf("Bytes avant=%s  apres=%s  economise=%s\n", human(bytesIn), human(bytesOut), human(saved))
	} else {
		fmt.Printf("[dry-run] economie estimee: %s (sur %d fichiers compresses)\n", human(saved), r.stats.done.Load())
	}
}

type stats struct {
	done, skipped, errs atomic.Int64
	bytesIn, bytesOut   atomic.Int64
}

// runner holds the fixed configuration shared by all workers, plus the
// counters they update concurrently.
type runner struct {
	srv     *drive.Service
	plog    *progressLog
	maxSize int
	apply   bool
	dumpDir string
	stats   stats
}

func (r *runner) processOne(ctx context.Context, f imgFile) {
	logErr := func(err error) {
		r.stats.errs.Add(1)
		_ = r.plog.write(logEntry{FileID: f.ID, Name: f.Name, OrigSize: f.Size, Error: err.Error(), DryRun: !r.apply})
		fmt.Fprintf(os.Stderr, "ERR %s (%s): %v\n", f.Name, f.ID, err)
	}

	in, err := download(ctx, r.srv, f.ID)
	if err != nil {
		logErr(fmt.Errorf("download: %w", err))
		return
	}

	out, strategy, err := compressJPEG(ctx, in, r.maxSize)
	if err != nil {
		logErr(fmt.Errorf("compress: %w", err))
		return
	}

	if r.dumpDir != "" {
		if err := dumpPair(r.dumpDir, f, in, out); err != nil {
			fmt.Fprintf(os.Stderr, "WARN dump %s: %v\n", f.Name, err)
		}
	}

	if len(out) >= len(in) {
		r.stats.skipped.Add(1)
		_ = r.plog.write(logEntry{FileID: f.ID, Name: f.Name, OrigSize: f.Size, NewSize: len(out), Skipped: "no_gain", DryRun: !r.apply})
		fmt.Printf("SKIP %s — recompressed=%s >= orig=%s\n", f.Name, human(int64(len(out))), human(f.Size))
		return
	}

	r.stats.bytesIn.Add(f.Size)
	r.stats.bytesOut.Add(int64(len(out)))

	if !r.apply {
		r.stats.done.Add(1)
		_ = r.plog.write(logEntry{FileID: f.ID, Name: f.Name, OrigSize: f.Size, NewSize: len(out), Strategy: strategy, DryRun: true})
		fmt.Printf("DRY  %s  %s -> %s  (%s)\n", f.Name, human(f.Size), human(int64(len(out))), strategy)
		return
	}

	if err := replaceContent(ctx, r.srv, f.ID, "image/jpeg", f.ModifiedTime, out); err != nil {
		logErr(fmt.Errorf("replace: %w", err))
		return
	}
	r.stats.done.Add(1)
	_ = r.plog.write(logEntry{FileID: f.ID, Name: f.Name, OrigSize: f.Size, NewSize: len(out), Strategy: strategy})
	fmt.Printf("OK   %s  %s -> %s  (%s)\n", f.Name, human(f.Size), human(int64(len(out))), strategy)
}

// printQuota fetches live Drive storage usage via about.get and prints the
// breakdown. Numbers are typically updated within seconds of any operation —
// much fresher than the Drive web UI which can lag 24-48 h.
func printQuota(ctx context.Context, srv *drive.Service) {
	about, err := srv.About.Get().Fields("storageQuota,user").Context(ctx).Do()
	if err != nil {
		log.Fatalf("about.get: %v", err)
	}
	q := about.StorageQuota
	fmt.Printf("Compte: %s <%s>\n", about.User.DisplayName, about.User.EmailAddress)
	fmt.Printf("Limite totale  : %s\n", human(q.Limit))
	fmt.Printf("Usage total    : %s\n", human(q.Usage))
	fmt.Printf("  dans Drive   : %s\n", human(q.UsageInDrive))
	fmt.Printf("  dans corbeille: %s\n", human(q.UsageInDriveTrash))
	if q.Limit > 0 {
		fmt.Printf("Pourcentage    : %.1f %%\n", 100*float64(q.Usage)/float64(q.Limit))
	}
}

// dumpPair writes the original and recompressed bytes side by side under
// dumpDir, using the Drive file ID as a unique prefix so duplicate names
// don't collide.
func dumpPair(dumpDir string, f imgFile, orig, recompressed []byte) error {
	prefix := f.ID + "_"
	base := f.Name
	origPath := filepath.Join(dumpDir, prefix+"orig_"+base)
	newPath := filepath.Join(dumpDir, prefix+"new_"+base)
	if err := os.WriteFile(origPath, orig, 0o644); err != nil {
		return err
	}
	return os.WriteFile(newPath, recompressed, 0o644)
}

func human(n int64) string {
	const u = 1024
	if n < u {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(u), 0
	for x := n / u; x >= u; x /= u {
		div *= u
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
