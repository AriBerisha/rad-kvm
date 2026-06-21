// Command qkvm is the q6a-kvm control plane: it serves the web UI, proxies the
// MJPEG video from ustreamer (single origin), and bridges browser keyboard/mouse
// input to the USB HID gadget (/dev/hidg*) so the browser drives the target.
//
// Single-user auth gates the video and input; the static page itself is public
// (it holds no secrets and renders the login/setup form).
package main

import (
	"embed"
	"encoding/json"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

//go:embed web
var webFS embed.FS

func main() {
	addr := flag.String("addr", ":8000", "HTTP listen address")
	stream := flag.String("stream", "http://127.0.0.1:8080", "ustreamer base URL to proxy (video)")
	kbdDev := flag.String("kbd", "/dev/hidg0", "HID keyboard gadget device")
	mouseDev := flag.String("mouse", "/dev/hidg1", "HID absolute-mouse gadget device")
	authFile := flag.String("auth-file", "/var/lib/q6a-kvm/auth.json", "credentials store (0600 JSON)")
	dataDir := flag.String("data-dir", "/var/lib/q6a-kvm", "dir for devices.json / macros.json")
	flag.Parse()

	hid, err := NewHID(*kbdDev, *mouseDev)
	if err != nil {
		log.Fatalf("open HID gadget devices: %v\n(is the gadget up — /dev/hidg0,1 — and are you root?)", err)
	}
	defer hid.Close()

	auth, err := NewAuth(*authFile)
	if err != nil {
		log.Fatalf("auth store %s: %v", *authFile, err)
	}

	store := NewStore(*dataDir)

	mux := http.NewServeMux()

	// static frontend (public — renders login/setup; holds no secrets)
	sub, _ := fs.Sub(webFS, "web")
	mux.Handle("/", http.FileServer(http.FS(sub)))

	// video proxy + input — GATED
	target, err := url.Parse(*stream)
	if err != nil {
		log.Fatalf("bad -stream URL: %v", err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1 // flush every write — required for MJPEG streaming
	mux.Handle("/stream", auth.requireAuth(proxy))
	mux.Handle("/snapshot", auth.requireAuth(proxy))
	mux.Handle("/ws/input", auth.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveInput(w, r, hid)
	})))

	// source status (for the UI to explain "no live video"): polls ustreamer
	mux.Handle("/api/source", auth.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sourceStatus(w, *stream)
	})))

	// auth API
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]bool{"configured": auth.Configured(), "authed": auth.valid(r)})
	})
	mux.HandleFunc("/api/setup", func(w http.ResponseWriter, r *http.Request) {
		c, ok := readCreds(w, r)
		if !ok {
			return
		}
		if err := auth.Setup(c.Username, c.Password); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		auth.issue(w)
		w.WriteHeader(http.StatusNoContent)
		log.Printf("account created: %q", c.Username)
	})
	mux.HandleFunc("/api/login", func(w http.ResponseWriter, r *http.Request) {
		c, ok := readCreds(w, r)
		if !ok {
			return
		}
		if !auth.Verify(c.Username, c.Password) {
			time.Sleep(500 * time.Millisecond) // throttle brute force
			http.Error(w, "invalid username or password", http.StatusUnauthorized)
			return
		}
		auth.issue(w)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/logout", func(w http.ResponseWriter, r *http.Request) {
		auth.clear(w)
		w.WriteHeader(http.StatusNoContent)
	})

	// device (Wake-on-LAN) + macro API
	registerAPI(mux, auth, store, hid)

	log.Printf("q6a-kvm control plane listening on %s", *addr)
	log.Printf("  video %s ; HID -> %s + %s ; auth %s", *stream, *kbdDev, *mouseDev, *authFile)
	if !auth.Configured() {
		log.Printf("  no account yet — first visit to the UI will prompt to create one")
	}
	log.Fatal(http.ListenAndServe(*addr, mux))
}

type creds struct{ Username, Password string }

// readCreds decodes {username,password} JSON, enforcing POST and a size limit.
func readCreds(w http.ResponseWriter, r *http.Request) (creds, bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return creds{}, false
	}
	var c struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&c) != nil || c.Username == "" || c.Password == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return creds{}, false
	}
	return creds{c.Username, c.Password}, true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// sourceStatus reports the capture source state by polling ustreamer's /state,
// so the UI can distinguish "no signal" from "online" and surface the detected
// resolution (e.g. to explain a too-high source).
func sourceStatus(w http.ResponseWriter, streamURL string) {
	out := map[string]any{"online": false}
	client := http.Client{Timeout: 2 * time.Second}
	if resp, err := client.Get(streamURL + "/state"); err == nil {
		defer resp.Body.Close()
		var s struct {
			Result struct {
				Source struct {
					Online      bool `json:"online"`
					CapturedFPS int  `json:"captured_fps"`
					Resolution  struct {
						Width  int `json:"width"`
						Height int `json:"height"`
					} `json:"resolution"`
				} `json:"source"`
			} `json:"result"`
		}
		if json.NewDecoder(resp.Body).Decode(&s) == nil {
			out["online"] = s.Result.Source.Online
			out["fps"] = s.Result.Source.CapturedFPS
			out["width"] = s.Result.Source.Resolution.Width
			out["height"] = s.Result.Source.Resolution.Height
		}
	}
	writeJSON(w, out)
}

// serveInput upgrades to WebSocket and pumps input messages into the HID bridge.
func serveInput(w http.ResponseWriter, r *http.Request, hid *HID) {
	c, err := wsUpgrade(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer c.Close()
	log.Printf("input client connected: %s", r.RemoteAddr)
	hid.Reset() // release everything on (re)connect so we never get stuck keys
	for {
		msg, err := c.ReadMessage()
		if err != nil {
			break
		}
		hid.Handle(msg)
	}
	hid.Reset()
	log.Printf("input client disconnected: %s", r.RemoteAddr)
}
