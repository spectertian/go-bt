// Package web provides the HTTP server and HTML templates for the magnet
// search website.
package web

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/spectertian/go-bt/internal/store"
)

// Server is the HTTP server for the magnet search website.
type Server struct {
	db   *store.DB
	addr string
	mux  *http.ServeMux
}

// funcs are custom template helper functions shared across all pages.
var funcs = template.FuncMap{
	"formatSize": formatSize,
	"add":        func(a, b int) int { return a + b },
	"sub":        func(a, b int) int { return a - b },
	"minInt": func(a, b int) int {
		if a < b {
			return a
		}
		return b
	},
	"maxInt": func(a, b int) int {
		if a > b {
			return a
		}
		return b
	},
	"seq": func(n int) []int {
		s := make([]int, n)
		for i := range s {
			s[i] = i + 1
		}
		return s
	},
}

// NewServer creates a new Server.
func NewServer(db *store.DB, addr string) *Server {
	s := &Server{db: db, addr: addr}
	s.mux = http.NewServeMux()
	s.mux.HandleFunc("/", s.handleIndex)
	s.mux.HandleFunc("/search", s.handleSearch)
	s.mux.HandleFunc("/detail/", s.handleDetail)
	s.mux.HandleFunc("/api/stats", s.handleStats)
	return s
}

// Start starts the HTTP server. It blocks until the server fails.
func (s *Server) Start() error {
	log.Printf("web: server listening on %s", s.addr)
	return http.ListenAndServe(s.addr, s.mux)
}

// Handler returns the underlying http.Handler (for testing).
func (s *Server) Handler() http.Handler { return s.mux }

// ---- handlers ----

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	count, _ := s.db.Count()
	data := map[string]interface{}{
		"Count": count,
	}
	s.render(w, tmplIndex, data)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}

	result, err := s.db.Search(q, page, 20)
	if err != nil {
		log.Printf("web: search error: %v", err)
		http.Error(w, "search failed", http.StatusInternalServerError)
		return
	}

	totalPages := int(math.Ceil(float64(result.Total) / float64(result.PageSize)))
	if totalPages < 1 {
		totalPages = 1
	}

	// JSON API support.
	if r.URL.Query().Get("format") == "json" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
		return
	}

	data := map[string]interface{}{
		"Query":      q,
		"Result":     result,
		"Page":       page,
		"TotalPages": totalPages,
	}
	s.render(w, tmplSearch, data)
}

func (s *Server) handleDetail(w http.ResponseWriter, r *http.Request) {
	ih := strings.TrimPrefix(r.URL.Path, "/detail/")
	ih = strings.ToLower(strings.TrimSpace(ih))
	if len(ih) != 40 {
		http.Error(w, "invalid infohash", http.StatusBadRequest)
		return
	}

	torrent, err := s.db.Get(ih)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if torrent == nil {
		http.NotFound(w, r)
		return
	}

	magnet := fmt.Sprintf("magnet:?xt=urn:btih:%s&dn=%s", torrent.InfoHash,
		strings.ReplaceAll(torrent.Name, " ", "+"))

	data := map[string]interface{}{
		"Torrent": torrent,
		"Magnet":  magnet,
		// MagnetURL is a safe URL so html/template won't sanitize the magnet: scheme.
		"MagnetURL": template.URL(magnet), //nolint:gosec // magnet URIs are safe to use as href
	}
	s.render(w, tmplDetail, data)
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	count, _ := s.db.Count()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"total_torrents": count,
	})
}

func (s *Server) render(w http.ResponseWriter, src string, data interface{}) {
	t, err := template.New("page").Funcs(funcs).Parse(src)
	if err != nil {
		log.Printf("web: template parse error: %v", err)
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.Execute(w, data); err != nil {
		log.Printf("web: template execute error: %v", err)
	}
}

// ---- helpers ----

func formatSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// ---- shared CSS / header / footer ----

const sharedCSS = `
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif;background:#0f1117;color:#e2e8f0;min-height:100vh}
a{color:#63b3ed;text-decoration:none}a:hover{text-decoration:underline}
.container{max-width:900px;margin:0 auto;padding:0 16px}
header{background:#1a1d27;border-bottom:1px solid #2d3748;padding:12px 0}
header .container{display:flex;align-items:center;gap:16px}
.logo{font-size:1.4rem;font-weight:700;color:#63b3ed;white-space:nowrap}
.search-bar{flex:1;display:flex;gap:8px}
.search-bar input{flex:1;padding:8px 14px;border-radius:6px;border:1px solid #4a5568;background:#2d3748;color:#e2e8f0;font-size:1rem}
.search-bar input:focus{outline:none;border-color:#63b3ed}
.search-bar button{padding:8px 18px;border-radius:6px;border:none;background:#3182ce;color:#fff;font-size:1rem;cursor:pointer}
.search-bar button:hover{background:#2b6cb0}
.hero{text-align:center;padding:80px 0 40px}
.hero h1{font-size:2.5rem;font-weight:800;color:#63b3ed;margin-bottom:12px}
.hero p{color:#a0aec0;font-size:1.1rem;margin-bottom:32px}
.hero .big-search{display:flex;max-width:600px;margin:0 auto;gap:8px}
.hero .big-search input{flex:1;padding:14px 18px;border-radius:8px;border:2px solid #4a5568;background:#1a1d27;color:#e2e8f0;font-size:1.1rem}
.hero .big-search input:focus{outline:none;border-color:#63b3ed}
.hero .big-search button{padding:14px 28px;border-radius:8px;border:none;background:#3182ce;color:#fff;font-size:1.1rem;cursor:pointer}
.stats{display:inline-block;margin-top:20px;color:#a0aec0;font-size:0.95rem}
.stats span{color:#63b3ed;font-weight:600}
.results-info{padding:16px 0;color:#a0aec0;font-size:0.9rem}
.result-list{list-style:none;display:flex;flex-direction:column;gap:10px;padding:8px 0}
.result-item{background:#1a1d27;border:1px solid #2d3748;border-radius:8px;padding:14px 18px;transition:border-color .2s}
.result-item:hover{border-color:#63b3ed}
.result-item .name{font-size:1rem;font-weight:600;margin-bottom:6px;word-break:break-all}
.result-item .meta{font-size:0.8rem;color:#718096;display:flex;gap:16px;flex-wrap:wrap}
.result-item .meta .size{color:#68d391}
.result-item .meta .files{color:#fbd38d}
.pagination{display:flex;justify-content:center;gap:6px;padding:24px 0;flex-wrap:wrap}
.pagination a,.pagination span{display:inline-block;padding:6px 12px;border-radius:5px;border:1px solid #4a5568;color:#e2e8f0;font-size:0.9rem}
.pagination a:hover{background:#2d3748;text-decoration:none}
.pagination .current{background:#3182ce;border-color:#3182ce;color:#fff}
.detail-card{background:#1a1d27;border:1px solid #2d3748;border-radius:10px;padding:28px;margin:24px 0}
.detail-card h2{font-size:1.3rem;font-weight:700;margin-bottom:16px;word-break:break-all}
.detail-meta{display:flex;gap:24px;flex-wrap:wrap;margin-bottom:20px}
.detail-meta div{font-size:0.9rem;color:#a0aec0}
.detail-meta div strong{color:#e2e8f0;display:block;font-size:1rem}
.magnet-box{background:#0f1117;border:1px solid #2d3748;border-radius:6px;padding:12px 14px;font-family:monospace;font-size:0.8rem;word-break:break-all;color:#68d391;margin-bottom:16px}
.btn{display:inline-block;padding:10px 20px;border-radius:6px;border:none;background:#3182ce;color:#fff;font-size:0.95rem;cursor:pointer;text-decoration:none}
.btn:hover{background:#2b6cb0;text-decoration:none}
.btn-copy{background:#2d3748;margin-left:8px;cursor:pointer}
.btn-copy:hover{background:#4a5568}
.empty{text-align:center;padding:60px 0;color:#718096}
.empty p{font-size:1.1rem;margin-bottom:8px}
footer{text-align:center;padding:24px;color:#718096;font-size:0.85rem;border-top:1px solid #2d3748;margin-top:40px}
`

const sharedScript = `
<script>
document.querySelectorAll('.btn-copy').forEach(function(btn){
  btn.addEventListener('click',function(){
    var text = btn.getAttribute('data-copy');
    navigator.clipboard.writeText(text).then(function(){
      var orig = btn.textContent; btn.textContent='已复制!';
      setTimeout(function(){btn.textContent=orig;},1500);
    });
  });
});
</script>`

// ---- page templates ----

const tmplIndex = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1.0">
<title>磁力搜索 - 发现全球种子</title>
<style>` + sharedCSS + `</style>
</head>
<body>
<header>
  <div class="container">
    <a class="logo" href="/">🧲 磁力搜索</a>
    <form class="search-bar" action="/search" method="get">
      <input type="text" name="q" placeholder="搜索种子名称..." autocomplete="off">
      <button type="submit">搜索</button>
    </form>
  </div>
</header>
<main>
  <div class="container">
    <div class="hero">
      <h1>🧲 磁力搜索</h1>
      <p>通过 DHT 网络实时发现并索引全球磁力链接</p>
      <form class="big-search" action="/search" method="get">
        <input type="text" name="q" placeholder="输入关键词搜索种子..." autofocus autocomplete="off">
        <button type="submit">搜索</button>
      </form>
      <div class="stats">已收录 <span>{{.Count}}</span> 个磁力资源</div>
    </div>
  </div>
</main>
<footer><div class="container">磁力搜索 &copy; 2025 &middot; DHT 爬虫驱动 &middot; go-bt</div></footer>
` + sharedScript + `
</body>
</html>`

const tmplSearch = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1.0">
<title>搜索: {{.Query}} - 磁力搜索</title>
<style>` + sharedCSS + `</style>
</head>
<body>
<header>
  <div class="container">
    <a class="logo" href="/">🧲 磁力搜索</a>
    <form class="search-bar" action="/search" method="get">
      <input type="text" name="q" value="{{.Query}}" placeholder="搜索种子名称..." autocomplete="off">
      <button type="submit">搜索</button>
    </form>
  </div>
</header>
<main>
  <div class="container">
    {{if .Result}}
    <div class="results-info">
      {{if .Query}}关键词 <strong>"{{.Query}}"</strong> 共找到 <strong>{{.Result.Total}}</strong> 个结果，
      第 {{.Page}} / {{.TotalPages}} 页{{else}}共收录 <strong>{{.Result.Total}}</strong> 个磁力资源{{end}}
    </div>
    {{if .Result.Items}}
    <ul class="result-list">
      {{range .Result.Items}}
      <li class="result-item">
        <div class="name"><a href="/detail/{{.InfoHash}}">{{.Name}}</a></div>
        <div class="meta">
          <span class="size">{{formatSize .Size}}</span>
          {{if .FileCount}}<span class="files">{{.FileCount}} 个文件</span>{{end}}
          <span>{{.CreatedAt.Format "2006-01-02"}}</span>
          <span>{{.InfoHash}}</span>
        </div>
      </li>
      {{end}}
    </ul>
    {{if gt .TotalPages 1}}
    <div class="pagination">
      {{if gt .Page 1}}<a href="/search?q={{.Query}}&page={{sub .Page 1}}">&laquo; 上一页</a>{{end}}
      {{range seq .TotalPages}}
        {{if eq . $.Page}}<span class="current">{{.}}</span>
        {{else}}<a href="/search?q={{$.Query}}&page={{.}}">{{.}}</a>{{end}}
      {{end}}
      {{if lt .Page .TotalPages}}<a href="/search?q={{.Query}}&page={{add .Page 1}}">下一页 &raquo;</a>{{end}}
    </div>
    {{end}}
    {{else}}
    <div class="empty"><p>没有找到相关结果</p><p>换个关键词试试？</p></div>
    {{end}}
    {{end}}
  </div>
</main>
<footer><div class="container">磁力搜索 &copy; 2025 &middot; DHT 爬虫驱动 &middot; go-bt</div></footer>
` + sharedScript + `
</body>
</html>`

const tmplDetail = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1.0">
<title>{{.Torrent.Name}} - 磁力搜索</title>
<style>` + sharedCSS + `</style>
</head>
<body>
<header>
  <div class="container">
    <a class="logo" href="/">🧲 磁力搜索</a>
    <form class="search-bar" action="/search" method="get">
      <input type="text" name="q" placeholder="搜索种子名称..." autocomplete="off">
      <button type="submit">搜索</button>
    </form>
  </div>
</header>
<main>
  <div class="container">
    <div class="detail-card">
      <h2>{{.Torrent.Name}}</h2>
      <div class="detail-meta">
        <div><span>文件大小</span><strong>{{formatSize .Torrent.Size}}</strong></div>
        {{if .Torrent.FileCount}}<div><span>文件数量</span><strong>{{.Torrent.FileCount}} 个文件</strong></div>{{end}}
        <div><span>收录时间</span><strong>{{.Torrent.CreatedAt.Format "2006-01-02 15:04"}}</strong></div>
        <div><span>InfoHash</span><strong>{{.Torrent.InfoHash}}</strong></div>
      </div>
      <div class="magnet-box">{{.Magnet}}</div>
      <a class="btn" href="{{.MagnetURL}}">📥 立即下载</a>
      <button class="btn btn-copy" data-copy="{{.Magnet}}">📋 复制磁力链</button>
    </div>
    <p><a href="javascript:history.back()">&larr; 返回搜索结果</a></p>
  </div>
</main>
<footer><div class="container">磁力搜索 &copy; 2025 &middot; DHT 爬虫驱动 &middot; go-bt</div></footer>
` + sharedScript + `
</body>
</html>`
