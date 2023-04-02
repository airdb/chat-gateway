package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/render"
	"github.com/go-resty/resty/v2"
	"go.uber.org/fx"
	"golang.org/x/exp/slog"
)

type proxyDeps struct {
	fx.In

	Logger *slog.Logger
}

type Proxy struct {
	deps *proxyDeps

	mux    *chi.Mux
	server *http.Server
}

func NewRest(deps proxyDeps) *Proxy {
	mux := chi.NewRouter()
	mux.Use(middleware.Logger)
	mux.Use(render.SetContentType(render.ContentTypeHTML))

	return &Proxy{deps: &deps, mux: mux}
}

func (p *Proxy) Start() error {
	p.mux.Route("/v1", func(r chi.Router) {
		r.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
			log := p.deps.Logger.With("uri", r.URL.String())

			log.Debug("ping")
			fmt.Fprintf(w, "pong")
		})
		r.HandleFunc("/openai/*", func(w http.ResponseWriter, r *http.Request) {
			token := fmt.Sprintf("Bearer %s", os.Getenv("CHATGW_TOKEN"))
			if r.Header.Get("Authorization") != token {
				fmt.Fprintf(w, "token error")
				return
			}

			logEntry := p.deps.Logger.With("uri", r.URL.String())

			client := resty.New()
			uri := fmt.Sprintf("https://api.openai.com/%s", chi.URLParam(r, "*"))

			request := client.R().
				SetAuthToken(os.Getenv("OPENAI_KEY")).
				SetQueryString(r.URL.RawQuery)

			rDumper := bytes.NewBuffer(nil)
			body := io.TeeReader(r.Body, rDumper)
			request.SetBody(body)

			resp, err := request.Execute(r.Method, uri)
			p.parseBody(logEntry, rDumper.Bytes()).Debug("request body")

			if err != nil {
				panic(err)
			}

			p.parseBody(logEntry, resp.Body()).Debug("response body")
			w.Write(resp.Body())
		})
	})

	p.server = &http.Server{Addr: ":30120", Handler: p.mux}

	// Run the server
	return p.server.ListenAndServe()
}

func (w *Proxy) Stop() error {
	return w.server.Shutdown(context.TODO())
}

func (p *Proxy) parseBody(logEntry *slog.Logger, body []byte) *slog.Logger {
	if len(body) == 0 {
		return logEntry
	}
	data := map[string]any{}
	if err := json.Unmarshal(body, &data); err == nil {
		return logEntry.With("body", data)
	} else {
		return logEntry.With("body", string(body)).With("err", err)
	}
}
