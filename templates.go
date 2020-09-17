package main

import (
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"
)

func (a *App) getTpl() (*template.Template, error) {
	if a.jit {
		return a.buildTheme(a.cfg.Theme)
	}
	tpl, ok := a.tpls[a.cfg.Theme]
	if !ok {
		return nil, fmt.Errorf("theme %q not found", a.cfg.Theme)
	}
	return tpl, nil
}

func (a *App) buildTpls() (map[string]*template.Template, error) {
	res := map[string]*template.Template{}
	themes := []string{}
	err := a.themesBox.Walk("", func(path string, info os.FileInfo, err error) error {
		if path == "" || path == "." {
			return nil
		}
		if info.IsDir() {
			if strings.Count(path, "/") < 1 {
				themes = append(themes, filepath.Base(path))
			}
			return nil
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, theme := range themes {
		tpl, err := a.buildTheme(theme)
		if err != nil {
			return nil, err
		}
		res[theme] = tpl

	}
	return res, nil
}

func (a *App) buildTheme(theme string) (*template.Template, error) {
	if theme == "" {
		return nil, fmt.Errorf("template name must not be empty")
	}
	if _, err := a.themesBox.Open(theme); err != nil {
		return nil, err
	}
	tpl := template.New("")
	baseTheme := filepath.Join(theme, "templates")
	err := a.themesBox.Walk(baseTheme, func(path string, info os.FileInfo, err error) error {
		if info.IsDir() {
			return nil
		}
		s, err := a.themesBox.String(path)
		if err != nil {
			return err
		}
		tpl, err = tpl.Parse(s)
		if err != nil {
			return err
		}
		return nil
	})
	return tpl, err
}
