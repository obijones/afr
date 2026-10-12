package view

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/obijones/afr/internal/rules"
	"github.com/obijones/afr/internal/schema"
)

// The report page: one HTML file whose script draws the sessions from a
// JSON data block. The data is embedded once; the page loads nothing from
// the network and its Content-Security-Policy allows only its own inline
// style and script.
var (
	//go:embed report/page.html
	reportPage string
	//go:embed report/page.css
	reportCSS string
	//go:embed report/page.js
	reportJS string
)

// ReportSession is one session to put in a report.
type ReportSession struct {
	Events    []schema.Event
	Integrity string // verifier verdict: "ok" or what is wrong
	OK        bool   // the log verified
	Current   map[string][]schema.Highlight
}

// ReportOptions describes the report as a whole.
type ReportOptions struct {
	Version   string
	Generated time.Time
	Loc       *time.Location // time zone for dates and times (nil = local)
	Scope     string         // what was selected, for the header
}

type reportData struct {
	Version   string          `json:"version"`
	Generated string          `json:"generated"`
	Zone      string          `json:"zone"`
	Scope     string          `json:"scope"`
	Sessions  []reportSession `json:"sessions"`
}

type reportSession struct {
	ID          string       `json:"id"`
	Date        string       `json:"date"`
	Start       string       `json:"start"`
	Last        string       `json:"last"`
	User        string       `json:"user"`
	Host        string       `json:"host"`
	Cwd         string       `json:"cwd"`
	StartCwd    string       `json:"start_cwd,omitempty"`
	Agent       string       `json:"agent"`
	Models      []string     `json:"models"`
	Status      string       `json:"status"`
	Open        bool         `json:"open"`
	Turns       int          `json:"turns"`
	Tools       int          `json:"tools"`
	Failed      int          `json:"failed"`
	Denied      int          `json:"denied"`
	Notices     int          `json:"notices"`
	Integrity   string       `json:"integrity"`
	IntegrityOK bool         `json:"integrity_ok"`
	Current     int          `json:"current"`
	Lines       []reportLine `json:"lines"`
}

type reportLine struct {
	Time      string         `json:"time"`
	Kind      string         `json:"kind"`
	Turn      string         `json:"turn,omitempty"`
	AgentType string         `json:"agent_type,omitempty"`
	Text      string         `json:"text"`
	Tool      *ToolLine      `json:"tool,omitempty"`
	Marks     []reportMark   `json:"marks,omitempty"`
	Details   []reportDetail `json:"details,omitempty"`
}

type reportMark struct {
	Rule     string `json:"rule"`
	Severity string `json:"sev"`
	Detail   string `json:"detail,omitempty"`
	Current  bool   `json:"current,omitempty"`
}

type reportDetail struct {
	Label string `json:"label"`
	Body  string `json:"body"`
}

// Report writes one HTML page covering the given sessions. Every captured
// value is neutralized as in the terminal replay, and secrets are masked in
// summary lines; full values under each line are kept, since the reviewer
// needs them.
func Report(w io.Writer, sessions []ReportSession, o ReportOptions) error {
	if o.Loc == nil {
		o.Loc = time.Local
	}
	d := reportData{
		Version:   o.Version,
		Generated: o.Generated.In(o.Loc).Format("2006-01-02 15:04:05"),
		Zone:      o.Generated.In(o.Loc).Format("MST (-07:00)"),
		Scope:     o.Scope,
		Sessions:  []reportSession{},
	}
	for _, s := range sessions {
		m, err := Build(s.Events, s.Current)
		if err != nil {
			continue
		}
		sum := m.Summary
		agent := sum.Agent
		if len(sum.Versions) > 0 {
			agent += " " + strings.Join(sum.Versions, " -> ")
		}
		// Every captured value is neutralized, session fields included:
		// a folder name or end reason can carry control characters too.
		n := func(v string) string { return neutralize(v, false) }
		models := make([]string, len(sum.Models))
		for i, v := range sum.Models {
			models[i] = n(v)
		}
		rs := reportSession{
			ID: n(sum.Session), User: n(sum.User), Host: n(sum.Host), Cwd: n(sum.Cwd), Agent: n(agent), Models: models,
			Date:   sum.Started.In(o.Loc).Format("2006-01-02"),
			Start:  sum.Started.In(o.Loc).Format("15:04:05"),
			Last:   sum.LastSeen.In(o.Loc).Format("2006-01-02 15:04:05"),
			Status: n(sum.Status), Open: sum.Status == "open",
			Turns: sum.Turns, Tools: sum.ToolCalls, Notices: sum.Notices,
			Integrity: n(s.Integrity), IntegrityOK: s.OK, Current: m.CurrentCount,
		}
		if sum.LastCwd != "" && sum.LastCwd != sum.Cwd { // moved during the session
			rs.Cwd, rs.StartCwd = n(sum.LastCwd), n(sum.Cwd)
		}
		for _, l := range m.Lines {
			rl := reportLine{
				Time:      l.Event.TS.In(o.Loc).Format("15:04:05"),
				Kind:      l.Kind,
				Turn:      l.Turn,
				AgentType: neutralize(l.Event.AgentType, false),
				Text:      neutralize(rules.MaskSecrets(l.Text), false),
			}
			if t := l.Tool; t != nil {
				rl.Tool = &ToolLine{
					Name:    neutralize(t.Name, false),
					Summary: neutralize(rules.MaskSecrets(t.Summary), false),
					Status:  neutralize(rules.MaskSecrets(t.Status), false),
					Failed:  t.Failed,
					Denied:  t.Denied,
				}
				if t.Denied {
					rs.Denied++
				} else if t.Failed {
					rs.Failed++
				}
			}
			for _, mk := range l.Marks {
				rl.Marks = append(rl.Marks, reportMark{
					Rule: neutralize(mk.RuleID, false), Severity: mk.Severity,
					Detail: neutralize(mk.Detail, false), Current: mk.Current,
				})
			}
			for _, dt := range l.Details {
				if strings.TrimSpace(dt.Body) == "" {
					continue
				}
				body := strings.ReplaceAll(dt.Body, "\r\n", "\n")
				rl.Details = append(rl.Details, reportDetail{Label: dt.Label, Body: neutralize(body, true)})
			}
			rs.Lines = append(rs.Lines, rl)
		}
		d.Sessions = append(d.Sessions, rs)
	}

	// json.Marshal escapes <, >, &, U+2028 and U+2029, so captured text
	// can never close the data block or start markup.
	data, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if bytes.Contains(bytes.ToLower(data), []byte("</script")) {
		return fmt.Errorf("report: data block would close its script tag")
	}
	page := strings.NewReplacer(
		"{{CSP}}", "default-src 'none'; style-src "+cspHash(reportCSS)+"; script-src "+cspHash(reportJS)+"; img-src 'none'; base-uri 'none'; form-action 'none'",
		"{{CSS}}", reportCSS,
		"{{JS}}", reportJS,
	).Replace(reportPage)
	before, after, ok := strings.Cut(page, "{{DATA}}")
	if !ok {
		return fmt.Errorf("report: page template has no data slot")
	}
	for _, s := range []string{before, string(data), after} {
		if _, err := io.WriteString(w, s); err != nil {
			return err
		}
	}
	return nil
}

func cspHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return "'sha256-" + base64.StdEncoding.EncodeToString(h[:]) + "'"
}
