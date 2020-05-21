package main

import (
	"archive/zip"
	"encoding/base64"
	"flag"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/mux"
)

var templates = template.Must(template.ParseGlob("*.tmpl.html"))

type cache map[string]*io.ReadCloser

type file struct {
	Name     string
	Len      int
	Contents []string
}

type state struct {
	serv  *http.Server
	files map[string]*file
	cache atomic.Value
	mu    sync.Mutex
}

func (s *state) getImage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "max-age=84600, public")
	vars := mux.Vars(r)
	f := vars["file"]
	i, err := strconv.Atoi(vars["page"])
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	dec, err := base64.StdEncoding.DecodeString(f)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	zip, err := zip.OpenReader(string(dec))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer zip.Close()
	if i >= len(zip.File) || i < 0 {
		http.Error(w, http.StatusText(404), 404)
		return
	}
	rc, err := zip.File[i].Open()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, err = io.Copy(w, rc)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rc.Close()
}

func (s *state) getViewer(w http.ResponseWriter, r *http.Request) {
	f := mux.Vars(r)["file"]
	d := struct {
		Hash string
		File *file
	}{
		f,
		s.files[f],
	}
	templates.ExecuteTemplate(w, "file.tmpl.html", d)
}

func (s *state) getMain(w http.ResponseWriter, r *http.Request) {
	err := templates.ExecuteTemplate(w, "main.tmpl.html", s.files)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func main() {
	s := new(state)
	r := mux.NewRouter()
	r.HandleFunc("/", s.getMain)
	r.HandleFunc("/{file}", s.getViewer)
	r.HandleFunc("/{file}/{page}", s.getImage)
	s.serv = &http.Server{
		Handler:      r,
		Addr:         "0.0.0.0:8888",
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt)

	go func() {
		for {
			select {
			case <-c:
				fmt.Println("http: shutting down")
				s.serv.Close()
			}
		}
	}()

	files := make(map[string]*file)
	fmt.Println("init: reading files into map")

	root := flag.String("data", "data/", "path to the zip files")
	flag.Parse()

	err := filepath.Walk(*root, func(path string, info os.FileInfo, err error) error {
		if filepath.Ext(path) != ".zip" {
			return nil
		}
		f := new(file)
		f.Name = info.Name()
		zip, err := zip.OpenReader(path)
		if err != nil {
			return err
		}
		f.Len = len(zip.File) - 1
		for _, file := range zip.File {
			f.Contents = append(f.Contents, file.Name)
		}
		zip.Close()
		files[base64.StdEncoding.EncodeToString([]byte(path))] = f
		return nil
	})
	if err != nil {
		panic(err)
	}
	s.files = files

	s.cache.Store(make(cache))

	fmt.Printf("http: listening on %s\n", s.serv.Addr)
	s.serv.ListenAndServe()
}
