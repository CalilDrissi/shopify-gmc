package web

import (
	"fmt"
	"html/template"
	"io/fs"
	"path/filepath"
	"sync"
)

// PageDef declares a page (layout + content template).
type PageDef struct {
	Name     string
	Layout   string
	Template string
}

type Renderer struct {
	mu       sync.RWMutex
	pages    map[string]*template.Template
	partials []string
	funcs    template.FuncMap
}

func NewRenderer(funcs template.FuncMap) *Renderer {
	return &Renderer{pages: map[string]*template.Template{}, funcs: funcs}
}

func (r *Renderer) LoadPartials(glob string) error {
	matches, err := filepath.Glob(glob)
	if err != nil {
		return err
	}
	r.partials = matches
	return nil
}

func (r *Renderer) Register(defs []PageDef) error {
	for _, d := range defs {
		files := append([]string{d.Layout, d.Template}, r.partials...)
		t, err := template.New(d.Name).Funcs(r.funcs).ParseFiles(files...)
		if err != nil {
			return fmt.Errorf("parse %s: %w", d.Name, err)
		}
		r.pages[d.Name] = t
	}
	return nil
}

func (r *Renderer) Page(name string) (*template.Template, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.pages[name]
	if !ok {
		return nil, fmt.Errorf("renderer: unknown page %q", name)
	}
	return t, nil
}

// FilesIn lists files in dir matching pattern.
func FilesIn(dir, pattern string) ([]string, error) {
	return filepath.Glob(filepath.Join(dir, pattern))
}

var _ fs.FS = (*nopFS)(nil)

type nopFS struct{}

func (nopFS) Open(string) (fs.File, error) { return nil, fs.ErrNotExist }
