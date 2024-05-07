package views

import (
	"bytes"
	"html/template"
	"io/fs"
)

func renderTemplate(fs fs.FS, file string, args interface{}) (bytes.Buffer, error) {
	var tpl bytes.Buffer

	// read the block-kit definition as a go template
	t, err := template.ParseFS(fs, file)
	if err != nil {
		return tpl, err
	}

	// we render the view
	err = t.Execute(&tpl, args)
	if err != nil {
		return tpl, err
	}

	return tpl, nil
}

func renderTemplateFromPath(filename string, args interface{}) (bytes.Buffer, error) {
	var tpl bytes.Buffer

	t, err := template.ParseFiles(filename)
	if err != nil {
		return tpl, err
	}

	// we render the view
	err = t.Execute(&tpl, args)
	if err != nil {
		return tpl, err
	}

	return tpl, nil
}
