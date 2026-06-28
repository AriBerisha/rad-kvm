package main

// Persistent stores for saved targets (Wake-on-LAN) and macros, each a JSON
// file in the data dir. Small, single-user; a mutex is plenty.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
)

type Device struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MAC       string `json:"mac"`
	Broadcast string `json:"broadcast,omitempty"`
}

type Macro struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Script string `json:"script"`
}

type Store struct {
	mu      sync.Mutex
	dir     string
	devices []Device
	macros  []Macro
}

func NewStore(dir string) *Store {
	s := &Store{dir: dir, devices: []Device{}, macros: []Macro{}}
	loadJSON(filepath.Join(dir, "devices.json"), &s.devices)
	loadJSON(filepath.Join(dir, "macros.json"), &s.macros)
	return s
}

func loadJSON(path string, v any) {
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, v)
	}
}

func saveJSON(path string, v any) error {
	b, _ := json.MarshalIndent(v, "", "  ")
	return os.WriteFile(path, b, 0o600)
}

func newID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// --- devices ---

func (s *Store) ListDevices() []Device {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Device{}, s.devices...)
}

func (s *Store) AddDevice(name, mac, bcast string) (Device, error) {
	hw, err := net.ParseMAC(mac)
	if err != nil || len(hw) != 6 {
		return Device{}, errors.New("invalid MAC address (use AA:BB:CC:DD:EE:FF)")
	}
	if name == "" {
		name = hw.String()
	}
	d := Device{ID: newID(), Name: name, MAC: hw.String(), Broadcast: bcast}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.devices = append(s.devices, d)
	return d, saveJSON(filepath.Join(s.dir, "devices.json"), s.devices)
}

func (s *Store) DelDevice(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Device, 0, len(s.devices))
	for _, d := range s.devices {
		if d.ID != id {
			out = append(out, d)
		}
	}
	s.devices = out
	return saveJSON(filepath.Join(s.dir, "devices.json"), s.devices)
}

func (s *Store) DeviceByID(id string) (Device, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.devices {
		if d.ID == id {
			return d, true
		}
	}
	return Device{}, false
}

// --- macros ---

func (s *Store) ListMacros() []Macro {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Macro{}, s.macros...)
}

func (s *Store) AddMacro(name, script string) (Macro, error) {
	if _, err := parseMacro(script); err != nil {
		return Macro{}, err
	}
	if name == "" {
		name = "macro"
	}
	m := Macro{ID: newID(), Name: name, Script: script}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.macros = append(s.macros, m)
	return m, saveJSON(filepath.Join(s.dir, "macros.json"), s.macros)
}

func (s *Store) DelMacro(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Macro, 0, len(s.macros))
	for _, m := range s.macros {
		if m.ID != id {
			out = append(out, m)
		}
	}
	s.macros = out
	return saveJSON(filepath.Join(s.dir, "macros.json"), s.macros)
}

func (s *Store) MacroByID(id string) (Macro, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.macros {
		if m.ID == id {
			return m, true
		}
	}
	return Macro{}, false
}
