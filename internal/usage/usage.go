// Package usage counts what Claude actually used, from session transcripts.
//
// Transcripts are an internal Claude Code format, so parsing is defensive: a line that doesn't
// match is skipped, never an error. Results are cached per file (by size and mtime) so repeat
// scans only read new or changed transcripts.
package usage

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Stat is how often something was used and when last.
type Stat struct {
	Count int    `json:"count"`
	Last  string `json:"last"` // RFC 3339; compare as strings
}

// Keys look like "mcp:supabase", "skill:superpowers:brainstorming", "agent:Explore" and
// "file:/abs/path/to/memory.md".
type Usage map[string]Stat

func (u Usage) add(key, ts string) {
	s := u[key]
	s.Count++
	if ts > s.Last {
		s.Last = ts
	}
	u[key] = s
}

func (u Usage) merge(o Usage) {
	for k, v := range o {
		s := u[k]
		s.Count += v.Count
		if v.Last > s.Last {
			s.Last = v.Last
		}
		u[k] = s
	}
}

type cacheEntry struct {
	Size  int64 `json:"size"`
	Mtime int64 `json:"mtime"`
	Usage Usage `json:"usage"`
}

// Scan reads transcripts under projectsDir modified since `since`. cachePath may be "" to disable caching.
func Scan(projectsDir string, since time.Time, cachePath string) Usage {
	files, _ := filepath.Glob(filepath.Join(projectsDir, "*", "*.jsonl"))
	cache := map[string]cacheEntry{}
	if cachePath != "" {
		if b, err := os.ReadFile(cachePath); err == nil {
			_ = json.Unmarshal(b, &cache)
		}
	}
	type job struct {
		path string
		fi   os.FileInfo
	}
	jobs := make(chan job)
	var mu sync.Mutex
	next := map[string]cacheEntry{}
	total := Usage{}
	var wg sync.WaitGroup
	for i := 0; i < runtime.NumCPU(); i++ {
		wg.Go(func() {
			for j := range jobs {
				c, ok := cache[j.path]
				if !ok || c.Size != j.fi.Size() || c.Mtime != j.fi.ModTime().UnixNano() {
					c = cacheEntry{Size: j.fi.Size(), Mtime: j.fi.ModTime().UnixNano(), Usage: parseFile(j.path)}
				}
				mu.Lock()
				next[j.path] = c
				total.merge(c.Usage)
				mu.Unlock()
			}
		})
	}
	for _, f := range files {
		fi, err := os.Stat(f)
		if err != nil || fi.ModTime().Before(since) {
			continue
		}
		jobs <- job{f, fi}
	}
	close(jobs)
	wg.Wait()
	if cachePath != "" {
		if b, err := json.Marshal(next); err == nil {
			_ = os.MkdirAll(filepath.Dir(cachePath), 0o700)
			_ = os.WriteFile(cachePath, b, 0o600)
		}
	}
	return total
}

type line struct {
	Timestamp string `json:"timestamp"`
	Message   struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type block struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Input struct {
		FilePath     string `json:"file_path"`
		Skill        string `json:"skill"`
		Command      string `json:"command"`
		SubagentType string `json:"subagent_type"`
	} `json:"input"`
}

var toolUse = []byte(`"tool_use"`)

func parseFile(path string) Usage {
	u := Usage{}
	f, err := os.Open(path)
	if err != nil {
		return u
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		b := sc.Bytes()
		if !bytes.Contains(b, toolUse) { // cheap filter: most lines carry no tool call
			continue
		}
		var ln line
		if json.Unmarshal(b, &ln) != nil || len(ln.Message.Content) == 0 || ln.Message.Content[0] != '[' {
			continue
		}
		var blocks []block
		if json.Unmarshal(ln.Message.Content, &blocks) != nil {
			continue
		}
		for _, bl := range blocks {
			if bl.Type != "tool_use" {
				continue
			}
			if k := key(bl); k != "" {
				u.add(k, ln.Timestamp)
			}
		}
	}
	return u
}

func key(b block) string {
	switch {
	case strings.HasPrefix(b.Name, "mcp__"):
		server, _, _ := strings.Cut(strings.TrimPrefix(b.Name, "mcp__"), "__")
		return "mcp:" + server
	case b.Name == "Skill":
		if b.Input.Skill != "" {
			return "skill:" + b.Input.Skill
		}
		if b.Input.Command != "" {
			return "skill:" + b.Input.Command
		}
	case b.Name == "Task" || b.Name == "Agent":
		if b.Input.SubagentType != "" {
			return "agent:" + b.Input.SubagentType
		}
	case b.Name == "Read" && strings.Contains(b.Input.FilePath, string(filepath.Separator)+"memory"+string(filepath.Separator)):
		return "file:" + b.Input.FilePath
	}
	return ""
}
