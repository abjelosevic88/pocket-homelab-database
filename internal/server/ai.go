package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-database/internal/ai"
	"github.com/abjelosevic88/pocket-homelab-database/internal/engine"
	"github.com/abjelosevic88/pocket-homelab-database/internal/store"
)

// aiFor is the person's AI: Pocket AI through Core, their own
// OpenAI-compatible key, or nil with why.
func (s *Server) aiFor(ctx context.Context, uid int64, st store.Settings) (*ai.Client, string, string) {
	email := ""
	if u, err := s.store.GetUser(ctx, uid); err == nil {
		email = u.Email
	}
	pocket, status := s.core.SelectAI(ctx, email, st.AIProvider, st.OpenAIKey != "")
	if pocket != nil {
		if !status.Ready {
			return nil, "", "Pocket AI: " + status.Detail
		}
		c := ai.New("", "", "", 150*time.Second)
		c.Pocket = pocket.WithModel(st.AIPocketModel)
		return c, "pocket", ""
	}
	if st.OpenAIKey == "" {
		return nil, "", status.Detail
	}
	return ai.New(st.OpenAIBaseURL, st.OpenAIKey, st.OpenAIModel, 150*time.Second), "openai", ""
}

func (s *Server) handleAIStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, _ := s.store.GetSettings(ctx, actorID(ctx))
	cl, used, why := s.aiFor(ctx, actorID(ctx), st)
	writeJSON(w, http.StatusOK, map[string]any{"ready": cl != nil, "provider": used, "detail": why})
}

func (s *Server) handleAIModels(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, status := s.core.SelectAI(ctx, userFrom(ctx).Email, "pocket", false)
	if c == nil || !status.Ready {
		writeErr(w, http.StatusServiceUnavailable, status.Detail)
		return
	}
	models, err := c.Models(ctx)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, models)
}

func validAIProvider(v string) bool { return core.ValidAIProvider(v) }

func validPocketModel(v string) bool { return core.ValidInferenceModel(v) }

const aiVoice = `You are a careful database engineer helping someone with their own databases. You write correct, efficient queries for the exact engine and version given, using only the tables and columns in the schema provided; never invent tables or columns. Prefer reading queries; when the request is to change data, write the change precisely (with a WHERE clause) and say what it will do. Quote identifiers only when needed. Answer plainly, without filler.`

// schemaDigest describes a database's tables and columns for the model
// (up to 80 tables, within 15 seconds).
func (s *Server) schemaDigest(ctx context.Context, c *store.Connection, db string) (string, error) {
	q, err := s.openSQL(ctx, c, db, true)
	if err != nil {
		return "", err
	}
	info, _ := q.Server(ctx)
	schemas, err := q.Schemas(ctx)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Engine: %s %s", engine.EngineName(c.Engine), info.Version)
	if f := info.Extra["flavor"]; f != "" && c.Engine == engine.MySQL {
		fmt.Fprintf(&b, " (%s)", f)
	}
	if db != "" {
		fmt.Fprintf(&b, ", database %q", db)
	}
	b.WriteString(".\n")
	deadline := time.Now().Add(15 * time.Second)
	n := 0
	for _, sc := range schemas {
		for _, o := range sc.Objects {
			if n >= 80 || time.Now().After(deadline) {
				fmt.Fprintf(&b, "(more tables not listed)\n")
				return b.String(), nil
			}
			n++
			t, err := q.Table(ctx, sc.Name, o.Name)
			if err != nil {
				continue
			}
			name := o.Name
			if c.Engine == engine.Postgres && sc.Name != "public" {
				name = sc.Name + "." + o.Name
			}
			cols := make([]string, 0, len(t.Columns))
			for _, col := range t.Columns {
				x := col.Name + " " + col.Type
				if col.PK {
					x += " PK"
				}
				if !col.Nullable {
					x += " NOT NULL"
				}
				cols = append(cols, x)
			}
			kind := ""
			if o.Kind != "table" {
				kind = " (" + o.Kind + ")"
			}
			rows := ""
			if o.Rows >= 0 {
				rows = fmt.Sprintf(", ~%d rows", o.Rows)
			}
			fmt.Fprintf(&b, "%s%s%s: %s\n", name, kind, rows, strings.Join(cols, ", "))
			for _, fk := range t.ForeignKeys {
				fmt.Fprintf(&b, "  %s(%s) -> %s(%s)\n", name, strings.Join(fk.Columns, ","), fk.RefTable, strings.Join(fk.RefColumns, ","))
			}
		}
	}
	return b.String(), nil
}

// handleAISQL writes a query from a request, explains one, or fixes one
// that failed. Nothing is run: the answer goes into the editor.
func (s *Server) handleAISQL(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	var in struct {
		ConnectionID int64  `json:"connection_id"`
		DB           string `json:"db"`
		Mode         string `json:"mode"` // write | explain | fix
		Prompt       string `json:"prompt"`
		SQL          string `json:"sql"`
		Error        string `json:"error"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	c, err := s.store.GetConnection(ctx, uid, in.ConnectionID)
	if err != nil {
		s.fail(w, err, "connection")
		return
	}
	st, _ := s.store.GetSettings(ctx, uid)
	cl, used, why := s.aiFor(ctx, uid, st)
	if cl == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": firstNonEmpty(why, "no AI is connected (Settings → AI)")})
		return
	}
	actx, cancel := context.WithTimeout(ctx, 150*time.Second)
	defer cancel()
	var digest string
	if c.Engine == engine.Redis {
		digest = "Engine: Redis (commands, not SQL). Database number " + firstNonEmpty(in.DB, "0") + "."
		if k, err := s.openKV(actx, c, in.DB, true); err == nil {
			if keys, _, err := k.Scan(actx, "*", 0, 60); err == nil && len(keys) > 0 {
				var parts []string
				for _, kk := range keys {
					parts = append(parts, kk.Key+" ("+kk.Type+")")
				}
				digest += " Some keys: " + strings.Join(parts, ", ") + "."
			}
		}
	} else if digest, err = s.schemaDigest(actx, c, in.DB); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "could not read the schema: " + err.Error()})
		return
	}
	lang := "SQL"
	if c.Engine == engine.Redis {
		lang = "Redis command (one per line)"
	}
	var prompt string
	switch in.Mode {
	case "explain":
		if strings.TrimSpace(in.SQL) == "" {
			writeErr(w, http.StatusBadRequest, "nothing to explain")
			return
		}
		prompt = "Explain in plain language what this " + lang + " does, step by step but briefly, and point out anything risky or slow. Answer as JSON {\"text\": \"…\"} (Markdown allowed in text).\n\nSchema:\n" + digest + "\n\nQuery:\n" + in.SQL
	case "fix":
		prompt = "This " + lang + " failed. Fix it. Answer as JSON {\"sql\": \"the corrected query\", \"note\": \"one sentence on what was wrong\"}.\n\nSchema:\n" + digest + "\n\nQuery:\n" + in.SQL + "\n\nError:\n" + in.Error
	default:
		if strings.TrimSpace(in.Prompt) == "" {
			writeErr(w, http.StatusBadRequest, "say what you want to see")
			return
		}
		prompt = "Write " + lang + " for this request: " + strings.TrimSpace(in.Prompt) + "\nLimit results to 200 rows unless asked otherwise. Answer as JSON {\"sql\": \"the query\", \"note\": \"one short sentence on what it shows or does\"}.\n\nSchema:\n" + digest
		if strings.TrimSpace(in.SQL) != "" {
			prompt += "\n\nThe editor currently holds (use it as context if relevant):\n" + in.SQL
		}
	}
	text, err := cl.Complete(actx, aiVoice, prompt, true)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	var out struct {
		SQL  string `json:"sql"`
		Note string `json:"note"`
		Text string `json:"text"`
	}
	raw := strings.TrimSpace(text)
	raw = strings.TrimPrefix(strings.TrimSuffix(strings.TrimPrefix(raw, "```json"), "```"), "```")
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &out); err != nil {
		out.Text = strings.TrimSpace(text)
	}
	out.SQL = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(out.SQL), "```sql"), "```"))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "provider": used, "sql": out.SQL, "note": out.Note, "text": out.Text})
}
