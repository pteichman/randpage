package main

import (
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pdfcpu/pdfcpu/pkg/api"
)

// randpage scans the files and paths passed on its command line for .pdf
// files, selecting a random one and opening it to a random page. It's a
// nice way to get a little incremental progress toward reading documents
// that are otherwise unseen.

func main() {
	var pdfs []string

	for _, arg := range os.Args[1:] {
		filepath.WalkDir(arg, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}

			if d.Type().IsRegular() && strings.HasSuffix(d.Name(), ".pdf") {
				pdfs = append(pdfs, path)
			}

			return nil
		})
	}

	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	rnd.Shuffle(len(pdfs), func(i, j int) {
		pdfs[i], pdfs[j] = pdfs[j], pdfs[i]
	})

	for len(pdfs) > 0 {
		path := pdfs[0]
		pdfs = pdfs[1:]

		nPages, err := countPages(path)
		if err != nil {
			continue
		}

		if err := execOpen(path, rnd.Intn(nPages)+1); err != nil {
			continue
		}

		// Success
		os.Exit(0)
	}

	fmt.Println("Could not find a usable PDF")
	os.Exit(1)
}

func countPages(path string) (int, error) {
	doc, err := api.ReadContextFile(path)
	if err != nil {
		return 0, err
	}

	return doc.XRefTable.PageCount, nil
}

// execOpen opens a pdf to the requested page. The browsers don't seem to
// support the `#page=N` argument on file urls, so this spawns a temporary
// web server to serve the pdf once. This function blocks until that
// transfer completes.
func execOpen(path string, page int) error {
	buf, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	port := ln.Addr().(*net.TCPAddr).Port

	var wg sync.WaitGroup
	wg.Add(1)

	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for len(buf) > 0 {
				n, err := w.Write(buf)
				if err != nil {
					fmt.Println(err)
				}
				buf = buf[n:]
			}
			wg.Done()
		}),
	}

	go srv.Serve(ln)

	// The actual string here doesn't matter, since the handler above
	// doesn't check the path. But this makes things look a little nicer in
	// the browser.
	filename := url.PathEscape(filepath.Base(path))
	url := fmt.Sprintf("http://127.0.0.1:%d/%s#page=%d", port, filename, page)

	cmd := exec.Command("open", url)
	if err := cmd.Run(); err != nil {
		return err
	}

	wg.Wait()
	return nil
}
