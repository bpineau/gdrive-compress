package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
)

func configDir() (string, error) {
	d, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(d, "gdrive-compress")
	if err := os.MkdirAll(p, 0o700); err != nil {
		return "", err
	}
	return p, nil
}

func loadOAuthConfig() (*oauth2.Config, error) {
	b, err := os.ReadFile("credentials.json")
	if err != nil {
		return nil, fmt.Errorf("credentials.json not found in working dir — see README for OAuth setup: %w", err)
	}
	cfg, err := google.ConfigFromJSON(b, drive.DriveScope)
	if err != nil {
		return nil, fmt.Errorf("parse credentials.json: %w", err)
	}
	return cfg, nil
}

func tokenPath() (string, error) {
	d, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "token.json"), nil
}

func loadToken() (*oauth2.Token, error) {
	p, err := tokenPath()
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	t := &oauth2.Token{}
	if err := json.NewDecoder(f).Decode(t); err != nil {
		return nil, err
	}
	return t, nil
}

func saveToken(t *oauth2.Token) error {
	p, err := tokenPath()
	if err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(t)
}

func openBrowser(u string) {
	var cmd string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "linux":
		cmd = "xdg-open"
	case "windows":
		cmd = "rundll32"
	}
	if cmd != "" {
		_ = exec.Command(cmd, u).Start()
	}
}

func interactiveAuth(ctx context.Context, cfg *oauth2.Config) (*oauth2.Token, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	cfg.RedirectURL = fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	state := oauth2.GenerateVerifier()
	authURL := cfg.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)

	type result struct {
		code string
		err  error
	}
	ch := make(chan result, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("state") != state {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			ch <- result{err: fmt.Errorf("state mismatch")}
			return
		}
		if e := q.Get("error"); e != "" {
			http.Error(w, "auth error: "+e, http.StatusBadRequest)
			ch <- result{err: fmt.Errorf("oauth error: %s", e)}
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body><h2>Authentification reussie.</h2><p>Vous pouvez fermer cet onglet.</p></body></html>")
		ch <- result{code: q.Get("code")}
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Shutdown(ctx)

	fmt.Println("Ouverture du navigateur pour authentification…")
	fmt.Println("Si ça ne s'ouvre pas, copie-colle :", authURL)
	openBrowser(authURL)

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-ch:
		if res.err != nil {
			return nil, res.err
		}
		tok, err := cfg.Exchange(ctx, res.code)
		if err != nil {
			return nil, fmt.Errorf("token exchange: %w", err)
		}
		return tok, nil
	}
}

func getClient(ctx context.Context) (*http.Client, error) {
	cfg, err := loadOAuthConfig()
	if err != nil {
		return nil, err
	}
	tok, err := loadToken()
	if err != nil {
		tok, err = interactiveAuth(ctx, cfg)
		if err != nil {
			return nil, err
		}
		if err := saveToken(tok); err != nil {
			return nil, err
		}
	}
	ts := cfg.TokenSource(ctx, tok)
	refreshed, err := ts.Token()
	if err != nil {
		p, _ := tokenPath()
		return nil, fmt.Errorf("refresh token (le token est peut-etre expire, supprime %s et relance): %w", p, err)
	}
	if refreshed.AccessToken != tok.AccessToken {
		_ = saveToken(refreshed)
	}
	return oauth2.NewClient(ctx, ts), nil
}
