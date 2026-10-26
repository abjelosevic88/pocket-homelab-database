// Package discover finds the databases running on this server: database
// containers (with the credentials in their environment) and SQLite files
// inside the folders containers mount. It talks to the Docker API over the
// socket and never changes anything except stopping and starting a
// container when asked to (the safe way to change a live SQLite file).
package discover

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

// Docker is a small client for the Docker (or Podman) API.
type Docker struct {
	client  *http.Client
	base    string
	missing bool
}

// NewDocker connects to host ("" = unix:///var/run/docker.sock).
func NewDocker(host string) *Docker {
	d := &Docker{}
	if host == "" {
		host = "unix:///var/run/docker.sock"
	}
	switch {
	case host == "off" || host == "none":
		d.missing = true
		d.client = &http.Client{}
	case strings.HasPrefix(host, "unix://"):
		path := strings.TrimPrefix(host, "unix://")
		if _, err := os.Stat(path); err != nil {
			d.missing = true
		}
		d.client = &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var dl net.Dialer
				return dl.DialContext(ctx, "unix", path)
			},
		}}
		d.base = "http://docker"
	case strings.HasPrefix(host, "tcp://"):
		d.client = &http.Client{Timeout: 60 * time.Second}
		d.base = "http://" + strings.TrimPrefix(host, "tcp://")
	default:
		d.client = &http.Client{Timeout: 60 * time.Second}
		d.base = strings.TrimRight(host, "/")
	}
	return d
}

// Available reports whether there is a container engine to talk to.
func (d *Docker) Available() bool { return d != nil && !d.missing }

func (d *Docker) do(ctx context.Context, method, path string, out any) error {
	if !d.Available() {
		return fmt.Errorf("no container engine: mount /var/run/docker.sock to find databases in containers")
	}
	req, err := http.NewRequestWithContext(ctx, method, d.base+path, nil)
	if err != nil {
		return err
	}
	res, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("container engine: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotModified {
		return nil
	}
	if res.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		var e struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(b, &e) == nil && e.Message != "" {
			return fmt.Errorf("container engine: %s", e.Message)
		}
		return fmt.Errorf("container engine: %s", res.Status)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, res.Body)
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

// Mount is one volume or bind mount of a container.
type Mount struct {
	Type        string `json:"Type"`
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
	RW          bool   `json:"RW"`
}

// Container is what discovery needs to know about one container.
type Container struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Image       string            `json:"image"`
	Running     bool              `json:"running"`
	State       string            `json:"state"`
	Project     string            `json:"project"`
	Service     string            `json:"service"`
	NetworkMode string            `json:"network_mode"`
	IPs         []string          `json:"ips"`
	Ports       []int             `json:"ports"`     // ports the container exposes
	Published   map[int]int       `json:"published"` // container port → host port
	Mounts      []Mount           `json:"mounts"`
	Env         map[string]string `json:"-"`
	Cmd         []string          `json:"-"`
}

type listItem struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	Image  string            `json:"Image"`
	State  string            `json:"State"`
	Labels map[string]string `json:"Labels"`
	Ports  []struct {
		PrivatePort int `json:"PrivatePort"`
		PublicPort  int `json:"PublicPort"`
	} `json:"Ports"`
	HostConfig struct {
		NetworkMode string `json:"NetworkMode"`
	} `json:"HostConfig"`
	NetworkSettings struct {
		Networks map[string]struct {
			IPAddress string `json:"IPAddress"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
	Mounts []Mount `json:"Mounts"`
}

func (it listItem) container() Container {
	c := Container{ID: it.ID, Image: it.Image, State: it.State, Running: it.State == "running",
		Project: it.Labels["com.docker.compose.project"], Service: it.Labels["com.docker.compose.service"],
		NetworkMode: it.HostConfig.NetworkMode, Mounts: it.Mounts, Published: map[int]int{}}
	if len(it.Names) > 0 {
		c.Name = strings.TrimPrefix(it.Names[0], "/")
	}
	for _, n := range it.NetworkSettings.Networks {
		if n.IPAddress != "" {
			c.IPs = append(c.IPs, n.IPAddress)
		}
	}
	sort.Strings(c.IPs)
	for _, p := range it.Ports {
		if p.PublicPort > 0 {
			c.Published[p.PrivatePort] = p.PublicPort
		}
	}
	return c
}

// Containers lists every container (running or not).
func (d *Docker) Containers(ctx context.Context) ([]Container, error) {
	var items []listItem
	if err := d.do(ctx, http.MethodGet, "/containers/json?all=1", &items); err != nil {
		return nil, err
	}
	out := make([]Container, 0, len(items))
	for _, it := range items {
		out = append(out, it.container())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

type inspectResp struct {
	ID    string `json:"Id"`
	Name  string `json:"Name"`
	State struct {
		Status  string `json:"Status"`
		Running bool   `json:"Running"`
	} `json:"State"`
	Config struct {
		Image        string              `json:"Image"`
		Env          []string            `json:"Env"`
		Cmd          []string            `json:"Cmd"`
		Entrypoint   []string            `json:"Entrypoint"`
		Labels       map[string]string   `json:"Labels"`
		ExposedPorts map[string]struct{} `json:"ExposedPorts"`
	} `json:"Config"`
	HostConfig struct {
		NetworkMode string `json:"NetworkMode"`
	} `json:"HostConfig"`
	NetworkSettings struct {
		Networks map[string]struct {
			IPAddress string `json:"IPAddress"`
		} `json:"Networks"`
		Ports map[string][]struct {
			HostPort string `json:"HostPort"`
		} `json:"Ports"`
	} `json:"NetworkSettings"`
	Mounts []Mount `json:"Mounts"`
}

// Inspect reads one container in full (environment and command included).
func (d *Docker) Inspect(ctx context.Context, name string) (*Container, error) {
	var r inspectResp
	if err := d.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(name)+"/json", &r); err != nil {
		return nil, err
	}
	c := &Container{ID: r.ID, Name: strings.TrimPrefix(r.Name, "/"), Image: r.Config.Image, Running: r.State.Running, State: r.State.Status,
		Project: r.Config.Labels["com.docker.compose.project"], Service: r.Config.Labels["com.docker.compose.service"],
		NetworkMode: r.HostConfig.NetworkMode, Mounts: r.Mounts, Env: map[string]string{}, Published: map[int]int{},
		Cmd: append(append([]string{}, r.Config.Entrypoint...), r.Config.Cmd...)}
	for _, kv := range r.Config.Env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			c.Env[k] = v
		}
	}
	for _, n := range r.NetworkSettings.Networks {
		if n.IPAddress != "" {
			c.IPs = append(c.IPs, n.IPAddress)
		}
	}
	sort.Strings(c.IPs)
	for p := range r.Config.ExposedPorts {
		var n int
		if _, err := fmt.Sscanf(p, "%d/tcp", &n); err == nil {
			c.Ports = append(c.Ports, n)
		}
	}
	sort.Ints(c.Ports)
	for p, binds := range r.NetworkSettings.Ports {
		var n, h int
		if _, err := fmt.Sscanf(p, "%d/tcp", &n); err != nil {
			continue
		}
		for _, b := range binds {
			if _, err := fmt.Sscanf(b.HostPort, "%d", &h); err == nil && h > 0 {
				c.Published[n] = h
				break
			}
		}
	}
	return c, nil
}

// Address is how this app reaches a container's port: its address on the
// container network, or the host itself for a container on the host network.
func (c *Container) Address(port int) (string, int) {
	if c.NetworkMode == "host" {
		return "127.0.0.1", port
	}
	if len(c.IPs) > 0 {
		return c.IPs[0], port
	}
	if hp, ok := c.Published[port]; ok {
		return "127.0.0.1", hp
	}
	return "", port
}

// Stop stops a container (waiting up to wait for it to finish cleanly).
func (d *Docker) Stop(ctx context.Context, name string, wait time.Duration) error {
	secs := int(wait.Seconds())
	if secs <= 0 {
		secs = 20
	}
	return d.do(ctx, http.MethodPost, fmt.Sprintf("/containers/%s/stop?t=%d", url.PathEscape(name), secs), nil)
}

// Start starts a container.
func (d *Docker) Start(ctx context.Context, name string) error {
	return d.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(name)+"/start", nil)
}
