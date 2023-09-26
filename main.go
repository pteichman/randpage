package main

import (
	"bufio"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"log/slog"

	"github.com/pdfcpu/pdfcpu/pkg/api"
)

// randpage scans the files and paths passed on its command line for .pdf
// files, selecting a random one and opening it to a random page. It's a
// nice way to get a little incremental progress toward reading documents
// that are otherwise unseen.

func main() {
	var pdfs []string

	for _, arg := range os.Args[1:] {
		if arg == "-" {
			pdfs = append(pdfs, readLines(os.Stdin)...)
			continue
		}

		pdfs = append(pdfs, walkForPdfs(arg)...)
	}

	slog.Info("found candidate pdfs", "count", len(pdfs))

	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	rnd.Shuffle(len(pdfs), func(i, j int) {
		pdfs[i], pdfs[j] = pdfs[j], pdfs[i]
	})

	for len(pdfs) > 0 {
		path := pdfs[0]
		pdfs = pdfs[1:]

		nPages, err := countPages(path)
		if err != nil {
			slog.Info("counting pages", "path", path, "err", err)
			continue
		}

		// nPages is 0-indexed; the browsers want 1-indexed.
		page := rnd.Intn(nPages) + 1

		slog.Info("opening pdf", "path", path, "page", page)

		if err := open(path, rnd.Intn(nPages)+1); err != nil {
			slog.Error("opening pdf", "path", path, "err", err)
			continue
		}

		// Success
		os.Exit(0)
	}

	fmt.Println("Could not find a usable PDF")
	os.Exit(1)
}

func looksLikePdf(s string) bool {
	return strings.HasSuffix(strings.ToLower(s), ".pdf")
}

func walkForPdfs(arg string) []string {
	var ret []string

	filepath.WalkDir(arg, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.Type().IsRegular() && looksLikePdf(d.Name()) {
			ret = append(ret, path)
		}

		return nil
	})

	return ret
}

func readLines(r io.Reader) []string {
	var ret []string

	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		if looksLikePdf(scanner.Text()) {
			ret = append(ret, scanner.Text())
		}
	}

	if err := scanner.Err(); err != nil {
		slog.Error("reading lines", slog.Any("err", err))
	}

	return ret
}

func countPages(path string) (int, error) {
	doc, err := api.ReadContextFile(path)
	if err != nil {
		return 0, err
	}

	return doc.XRefTable.PageCount, nil
}

// open opens a pdf to the requested page. The browsers don't seem to
// support the `#page=N` argument on file urls, so this spawns a temporary
// web server to serve the pdf once. This function blocks until that
// transfer completes.
func open(path string, page int) error {
	buf, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		slog.Error("listening", "err", err)
		return err
	}
	port := ln.Addr().(*net.TCPAddr).Port

	var wg sync.WaitGroup
	wg.Add(1)

	filename := filepath.Base(path)
	url := fmt.Sprintf("http://127.0.0.1:%d/%s#page=%d", port, url.PathEscape(filename), page)

	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			slog.Info("http request", "method", r.Method, "path", r.URL.Path, "user-agent", r.Header.Get("User-Agent"))

			if r.URL.Path != "/"+filename {
				http.NotFound(w, r)
				return
			}

			w.Header().Set("Content-Type", "application/pdf")
			w.Header().Set("Content-Length", strconv.Itoa(len(buf)))

			buf := buf
			for len(buf) > 0 {
				n, err := w.Write(buf)
				if err != nil {
					slog.Error("writing response body", "path", path, "err", err)
					return
				}
				buf = buf[n:]
			}
			wg.Done()
		}),
	}

	go srv.Serve(ln)

	cmd := exec.Command("open", url)
	if err := cmd.Run(); err != nil {
		slog.Error("executing viewer", "url", url, "err", err)
		return err
	}

	wg.Wait()
	return nil
}
