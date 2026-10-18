package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

var version = "dev"

const (
	banner = "codemodify/simple-http-fileserver"

	// pulsePath answers with the banner and version. It touches neither the
	// folder nor the disk, so it is cheap enough to poll.
	pulsePath = "/pulse"

	defaultHTTPOn  = "127.0.0.1:64080"
	defaultHTTPSOn = "127.0.0.1:64043"
)

func init() {
	log.SetFlags(0)
}

func main() {
	workFolder := "."
	if cwd, err := os.Getwd(); err == nil {
		workFolder = cwd
	}

	// flags
	showVersion := flag.Bool("version", false, "print version and exit")
	folder := flag.String("folder", workFolder, "folder")

	enableHTTP := flag.Bool("enable-http", true, "enable HTTP listener")
	httpOn := flag.String("http-on", defaultHTTPOn, "HTTP bind address")

	enableHTTPS := flag.Bool("enable-https", false, "enable HTTPS listener")
	httpsOn := flag.String("https-on", defaultHTTPSOn, "HTTPS bind address")
	httpsCert := flag.String("https-cert", "", "HTTPS Cert")
	httpsKey := flag.String("https-key", "", "HTTPS Key")

	enableDirListing := flag.Bool("enable-dir-listing", false, "enable directory listing")
	enableFileNotFound := flag.Bool("enable-file-not-found", true, "serve --file-not-found-file for a missing file")
	fileNotFoundFile := flag.String("file-not-found-file", "404.html", "the not-found page, relative to --folder")

	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	if err := validateServerConfig(*enableHTTP, *enableHTTPS, *httpsCert, *httpsKey); err != nil {
		logError("%v", err)
	}

	handler := accessLog(withPulse(newFileHandler(*folder, *enableDirListing, *enableFileNotFound, *fileNotFoundFile)))

	if *enableHTTP {
		logInfo("listen | http://%s", *httpOn)
	}
	if *enableHTTPS {
		logInfo("listen | https://%s", *httpsOn)
	}
	logInfo("folder | %s", *folder)

	const (
		readHeaderTimeout = 10 * time.Second
		idleTimeout       = 120 * time.Second
		shutdownTimeout   = 5 * time.Second
	)

	var servers []*http.Server
	errCh := make(chan error, 2)

	if *enableHTTP {
		srv := &http.Server{
			Addr:              *httpOn,
			Handler:           handler,
			ReadHeaderTimeout: readHeaderTimeout,
			IdleTimeout:       idleTimeout,
		}
		servers = append(servers, srv)
		go func() {
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- fmt.Errorf("http server on %s: %w", srv.Addr, err)
			}
		}()
	}

	if *enableHTTPS {
		srv := &http.Server{
			Addr:              *httpsOn,
			Handler:           handler,
			ReadHeaderTimeout: readHeaderTimeout,
			IdleTimeout:       idleTimeout,
		}
		servers = append(servers, srv)
		go func() {
			if err := srv.ListenAndServeTLS(*httpsCert, *httpsKey); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- fmt.Errorf("https server on %s: %w", srv.Addr, err)
			}
		}()
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	var serverErr error
	select {
	case serverErr = <-errCh:
		logInfo("err    | %v", serverErr)
	case sig := <-sigCh:
		logInfo("signal | %s", sig)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	for _, srv := range servers {
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logInfo("err    | shutdown: %v", err)
		}
	}
	cancel()

	if serverErr != nil {
		os.Exit(1)
	}
}

func validateServerConfig(enableHTTP, enableHTTPS bool, httpsCert, httpsKey string) error {
	if !enableHTTP && !enableHTTPS {
		return errors.New("either '--enable-http' or '--enable-https' is required")
	}

	if enableHTTPS {
		if httpsCert == "" {
			return errors.New("'--https-cert' is required when '--enable-https' is set")
		}
		if httpsKey == "" {
			return errors.New("'--https-key' is required when '--enable-https' is set")
		}
	}

	return nil
}

// withPulse answers pulsePath ahead of the files, so a file named "pulse" at
// the top of --folder is not served.
func withPulse(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == pulsePath {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprintf(w, "%s %s\n", banner, version)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func newFileHandler(folder string, enableDirListing bool, enableFileNotFound bool, fileNotFoundFile string) http.Handler {
	var fileSystem http.FileSystem = http.Dir(folder)
	if !enableDirListing {
		fileSystem = noDirListingFileSystem{fileSystem}
	}
	fileServer := http.FileServer(fileSystem)

	if !enableFileNotFound {
		return fileServer
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &notFoundRecorder{
			ResponseWriter: w,
			header:         make(http.Header),
		}
		fileServer.ServeHTTP(rec, r)
		if rec.notFound {
			serveNotFoundFile(w, r, fileSystem, fileNotFoundFile)
		}
	})
}

type noDirListingFileSystem struct {
	fs http.FileSystem
}

func (ndfs noDirListingFileSystem) Open(name string) (http.File, error) {
	f, err := ndfs.fs.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if info.IsDir() {
		idx, err := ndfs.fs.Open(name + "/index.html")
		if err != nil {
			f.Close()
			return nil, os.ErrNotExist
		}
		idx.Close()
	}
	return f, nil
}

// notFoundRecorder wraps a ResponseWriter and intercepts 404 responses from
// http.FileServer so the handler can substitute a custom not-found page.
// It uses its own header map to prevent header leakage from the suppressed
// response into the replacement response.
type notFoundRecorder struct {
	http.ResponseWriter
	header     http.Header
	notFound   bool
	headerSent bool
}

func (rec *notFoundRecorder) Header() http.Header {
	return rec.header
}

func (rec *notFoundRecorder) WriteHeader(code int) {
	if rec.headerSent {
		return
	}
	rec.headerSent = true
	if code == http.StatusNotFound {
		rec.notFound = true
		return
	}
	dst := rec.ResponseWriter.Header()
	for k, vv := range rec.header {
		dst[k] = vv
	}
	rec.ResponseWriter.WriteHeader(code)
}

func (rec *notFoundRecorder) Write(b []byte) (int, error) {
	if !rec.headerSent {
		rec.WriteHeader(http.StatusOK)
	}
	if rec.notFound {
		return len(b), nil
	}
	return rec.ResponseWriter.Write(b)
}

func serveNotFoundFile(w http.ResponseWriter, r *http.Request, fileSystem http.FileSystem, name string) {
	file, err := fileSystem.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()

	fileInfo, err := file.Stat()
	if err != nil || fileInfo.IsDir() {
		http.NotFound(w, r)
		return
	}

	http.ServeContent(&staticStatusResponseWriter{
		ResponseWriter: w,
		statusCode:     http.StatusNotFound,
	}, r, fileInfo.Name(), fileInfo.ModTime(), file)
}

type staticStatusResponseWriter struct {
	http.ResponseWriter
	statusCode    int
	headerWritten bool
}

func (w *staticStatusResponseWriter) WriteHeader(statusCode int) {
	if w.headerWritten {
		return
	}

	w.headerWritten = true
	w.ResponseWriter.WriteHeader(w.statusCode)
}

func (w *staticStatusResponseWriter) Write(p []byte) (int, error) {
	if !w.headerWritten {
		w.WriteHeader(http.StatusOK)
	}

	return w.ResponseWriter.Write(p)
}

func accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logInfo("%s %s start", r.Method, r.URL.Path)
		rec := &statusRecorder{ResponseWriter: w}
		start := time.Now()
		next.ServeHTTP(rec, r)
		logInfo("%s %s  done - %d %s", r.Method, r.URL.Path, rec.status, time.Since(start).Round(time.Millisecond))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (rec *statusRecorder) WriteHeader(code int) {
	rec.status = code
	rec.ResponseWriter.WriteHeader(code)
}

func (rec *statusRecorder) Write(b []byte) (int, error) {
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	return rec.ResponseWriter.Write(b)
}

func logInfo(format string, v ...interface{}) {
	log.Default().Printf("I | %s | %s | %s | %s \n", banner, version, timeNowAsString(), fmt.Sprintf(format, v...))
}

func logError(format string, v ...interface{}) {
	log.Default().Fatalf("E | %s | %s | %s | %s \n", banner, version, timeNowAsString(), fmt.Sprintf(format, v...))
}

func timeNowAsString() string {
	return time.Now().UTC().Format(time.RFC3339)
}
