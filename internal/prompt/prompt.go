package prompt

import (
	"bytes"
	"embed"
	"fmt"
	"text/template"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

// 预解析所有模板（启动时一次性加载）
var templates *template.Template

func init() {
	templates = template.Must(
		template.New("").ParseFS(templateFS, "templates/*.tmpl"),
	)
}

// Render 渲染指定模板，data 是传入模板的变量 map 或 struct。
func Render(name string, data any) (string, error) {
	var buf bytes.Buffer
	if err := templates.ExecuteTemplate(&buf, name, data); err != nil {
		return "", fmt.Errorf("render template %s: %w", name, err)
	}
	return buf.String(), nil
}

// MustRender 渲染模板，失败时 panic（适用于不可能失败的场景）。
func MustRender(name string, data any) string {
	s, err := Render(name, data)
	if err != nil {
		panic(err)
	}
	return s
}
