package web

import (
	"encoding/json"
	"fmt"
	"github.com/khoryik96-creator/Garbage/internal/connectors/jobadder"
	"github.com/khoryik96-creator/Garbage/internal/domain"
	"net/http"
)

func (a *App) connectJobAdder(w http.ResponseWriter, r *http.Request) {
	if a.Options.JobAdder == nil {
		a.fail(w, r, domain.Invalid("Open the installed app to connect JobAdder."), 0)
		return
	}
	if err := formKeys(r, "client_id", "client_secret", "redirect_uri", "pkce", "remember"); err != nil {
		a.fail(w, r, err, 0)
		return
	}
	cfg := jobadder.Configuration{ClientID: r.PostForm.Get("client_id"), ClientSecret: r.PostForm.Get("client_secret"), RedirectURI: r.PostForm.Get("redirect_uri"), PKCE: r.PostForm.Get("pkce") == "1", Remember: r.PostForm.Get("remember") == "1"}
	if err := a.Options.JobAdder.Configure(cfg); err != nil {
		a.fail(w, r, err, 0)
		return
	}
	browser := domain.ID()
	address, err := a.Options.JobAdder.Start("http://"+r.Host, browser)
	if err != nil {
		a.fail(w, r, err, 0)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "gt_jobadder_flow", Value: browser, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 600})
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; form-action 'self' https://id.jobadder.com; frame-ancestors 'none'; base-uri 'none'")
	http.Redirect(w, r, address, 303)
}
func (a *App) disconnectJobAdder(w http.ResponseWriter, r *http.Request) {
	if a.Options.JobAdder == nil {
		a.fail(w, r, domain.Invalid("JobAdder is not configured."), 0)
		return
	}
	if err := formKeys(r, "_csrf"); err != nil {
		a.fail(w, r, err, 0)
		return
	}
	if err := a.Options.JobAdder.Disconnect(); err != nil {
		a.fail(w, r, err, 0)
		return
	}
	http.Redirect(w, r, "/settings", 303)
}
func (a *App) jobAdderProfiles(w http.ResponseWriter, r *http.Request) {
	if a.Options.JobAdder == nil {
		a.fail(w, r, domain.Invalid("Connect JobAdder in Settings first."), 0)
		return
	}
	page, err := a.Options.JobAdder.Candidates(r.Context(), r.URL.Query().Get("next"))
	if err != nil {
		a.fail(w, r, err, 0)
		return
	}
	profiles := []domain.Candidate{}
	for _, raw := range page.Items {
		var candidate struct {
			ID        int    `json:"candidateId"`
			FirstName string `json:"firstName"`
			LastName  string `json:"lastName"`
			Email     string `json:"email"`
			Address   struct {
				Country string `json:"country"`
			} `json:"address"`
		}
		if err = json.Unmarshal(raw, &candidate); err != nil {
			a.fail(w, r, domain.Invalid("JobAdder returned an unsupported candidate shape."), 0)
			return
		}
		name := candidate.FirstName + " " + candidate.LastName
		if name == " " {
			name = fmt.Sprintf("JobAdder profile #%d", candidate.ID)
		}
		profiles = append(profiles, domain.Candidate{ID: candidate.ID, Name: name, Country: domain.String(candidate.Address.Country), OtherFields: map[string]string{"email": candidate.Email}})
	}
	a.render(w, r, "profiles", map[string]any{"Live": true, "LiveTotal": page.TotalCount, "LiveNext": page.Links.Next, "Page": domain.CandidatePage{Candidates: profiles, Finished: true}}, 200)
}
