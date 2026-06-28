package main

// REST API for saved devices (Wake-on-LAN) and macros. All routes gated by auth.

import (
	"encoding/json"
	"net/http"
)

func registerAPI(mux *http.ServeMux, auth *Auth, store *Store, hid *HID) {
	gated := func(h http.HandlerFunc) http.Handler { return auth.requireAuth(h) }

	// /api/devices : GET list, POST {name,mac,broadcast} add
	mux.Handle("/api/devices", gated(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, store.ListDevices())
		case http.MethodPost:
			var in struct{ Name, MAC, Broadcast string }
			if !decodeJSON(w, r, &in) {
				return
			}
			d, err := store.AddDevice(in.Name, in.MAC, in.Broadcast)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, d)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}))

	// /api/devices/delete : POST {id}
	mux.Handle("/api/devices/delete", gated(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ ID string }
		if !decodeJSON(w, r, &in) {
			return
		}
		store.DelDevice(in.ID)
		w.WriteHeader(http.StatusNoContent)
	}))

	// /api/devices/wake : POST {id} -> send magic packet
	mux.Handle("/api/devices/wake", gated(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ ID string }
		if !decodeJSON(w, r, &in) {
			return
		}
		d, ok := store.DeviceByID(in.ID)
		if !ok {
			http.Error(w, "no such device", http.StatusNotFound)
			return
		}
		if err := SendWoL(d.MAC, d.Broadcast); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	// /api/macros : GET list, POST {name,script} add (validated)
	mux.Handle("/api/macros", gated(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, store.ListMacros())
		case http.MethodPost:
			var in struct{ Name, Script string }
			if !decodeJSON(w, r, &in) {
				return
			}
			m, err := store.AddMacro(in.Name, in.Script)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeJSON(w, m)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}))

	// /api/macros/delete : POST {id}
	mux.Handle("/api/macros/delete", gated(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ ID string }
		if !decodeJSON(w, r, &in) {
			return
		}
		store.DelMacro(in.ID)
		w.WriteHeader(http.StatusNoContent)
	}))

	// /api/macros/run : POST {id} -> execute on the HID (async)
	mux.Handle("/api/macros/run", gated(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ ID string }
		if !decodeJSON(w, r, &in) {
			return
		}
		m, ok := store.MacroByID(in.ID)
		if !ok {
			http.Error(w, "no such macro", http.StatusNotFound)
			return
		}
		steps, err := parseMacro(m.Script)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		go hid.RunMacro(steps)
		w.WriteHeader(http.StatusAccepted)
	}))
}

// decodeJSON enforces POST and decodes a small JSON body.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return false
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(v) != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return false
	}
	return true
}
