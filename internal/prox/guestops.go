package prox

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

func guestPath(node, typ string, vmid int) string {
	kind := "qemu"
	if typ == "lxc" || typ == "ct" {
		kind = "lxc"
	}
	return "/nodes/" + url.PathEscape(node) + "/" + kind + "/" + strconv.Itoa(vmid)
}

// GuestConfig returns the configuration of a VM or container (raw key/value map).
func (p *PVE) GuestConfig(ctx context.Context, node, typ string, vmid int) (map[string]any, error) {
	var out map[string]any
	err := p.Do(ctx, http.MethodGet, guestPath(node, typ, vmid)+"/config", nil, &out)
	return out, err
}

// SetGuestConfig changes options of a VM or container.
func (p *PVE) SetGuestConfig(ctx context.Context, node, typ string, vmid int, params Params) error {
	return p.Do(ctx, http.MethodPut, guestPath(node, typ, vmid)+"/config", params, nil)
}

// GuestStatus is GET …/status/current.
type GuestStatus struct {
	Status    string `json:"status"`    // running, stopped
	QMPStatus string `json:"qmpstatus"` // running, paused, …
	Agent     int    `json:"agent"`     // 1 = guest agent enabled in the config
	Uptime    int64  `json:"uptime"`
	Lock      string `json:"lock"`
}

// GuestStatus returns the current state of a guest.
func (p *PVE) GuestStatus(ctx context.Context, node, typ string, vmid int) (GuestStatus, error) {
	var st GuestStatus
	err := p.Do(ctx, http.MethodGet, guestPath(node, typ, vmid)+"/status/current", nil, &st)
	return st, err
}

// GuestAction starts, stops or shuts down a guest and returns the task.
func (p *PVE) GuestAction(ctx context.Context, node, typ string, vmid int, action string, params Params) (string, error) {
	var upid string
	err := p.Do(ctx, http.MethodPost, guestPath(node, typ, vmid)+"/status/"+action, params, &upid)
	return upid, err
}

// DestroyGuest deletes a guest with its disks (purge also removes it from jobs and HA).
func (p *PVE) DestroyGuest(ctx context.Context, node, typ string, vmid int) (string, error) {
	var upid string
	params := Params{"purge": true, "destroy-unreferenced-disks": true}
	if typ == "lxc" || typ == "ct" {
		params["force"] = true
	}
	err := p.Do(ctx, http.MethodDelete, guestPath(node, typ, vmid), params, &upid)
	return upid, err
}

// AgentPing asks the QEMU guest agent of a VM whether it answers.
func (p *PVE) AgentPing(ctx context.Context, node string, vmid int) error {
	return p.Do(ctx, http.MethodPost, guestPath(node, "qemu", vmid)+"/agent/ping", nil, nil)
}

// AgentOSInfo returns the pretty name of the guest OS (via the agent).
func (p *PVE) AgentOSInfo(ctx context.Context, node string, vmid int) (string, string) {
	var os struct {
		Result struct {
			PrettyName string `json:"pretty-name"`
			Name       string `json:"name"`
		} `json:"result"`
	}
	var host struct {
		Result struct {
			HostName string `json:"host-name"`
		} `json:"result"`
	}
	_ = p.Do(ctx, http.MethodGet, guestPath(node, "qemu", vmid)+"/agent/get-osinfo", nil, &os)
	_ = p.Do(ctx, http.MethodGet, guestPath(node, "qemu", vmid)+"/agent/get-host-name", nil, &host)
	name := os.Result.PrettyName
	if name == "" {
		name = os.Result.Name
	}
	return name, host.Result.HostName
}

// NodeStatus is GET /nodes/{node}/status (memory).
type PVENodeStatus struct {
	Memory struct {
		Total int64 `json:"total"`
		Used  int64 `json:"used"`
		Free  int64 `json:"free"`
	} `json:"memory"`
	CPUInfo struct {
		CPUs int `json:"cpus"`
	} `json:"cpuinfo"`
}

// NodeStatus returns memory and CPUs of a node.
func (p *PVE) NodeStatus(ctx context.Context, node string) (PVENodeStatus, error) {
	var st PVENodeStatus
	err := p.Do(ctx, http.MethodGet, "/nodes/"+url.PathEscape(node)+"/status", nil, &st)
	return st, err
}

// StorageStatus returns total/used/avail of a storage on a node.
func (p *PVE) StorageStatus(ctx context.Context, node, storage string) (NodeStorage, error) {
	var st NodeStorage
	err := p.Do(ctx, http.MethodGet, "/nodes/"+url.PathEscape(node)+"/storage/"+url.PathEscape(storage)+"/status", nil, &st)
	return st, err
}

// Bridges lists the bridges (Linux bridge, OVS, SDN vnets) of a node.
func (p *PVE) Bridges(ctx context.Context, node string) ([]string, error) {
	var list []struct {
		Iface string `json:"iface"`
	}
	if err := p.Do(ctx, http.MethodGet, "/nodes/"+url.PathEscape(node)+"/network", Params{"type": "any_bridge"}, &list); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(list))
	for _, b := range list {
		out = append(out, b.Iface)
	}
	return out, nil
}

// StopTask aborts a running task.
func (p *PVE) StopTask(ctx context.Context, upid string) error {
	return p.Do(ctx, http.MethodDelete, "/nodes/"+url.PathEscape(UPIDNode(upid))+"/tasks/"+url.PathEscape(upid), nil, nil)
}

// WaitTaskCtx waits for a task until it stops or the context ends.
func (p *PVE) WaitTaskCtx(ctx context.Context, upid string) (TaskStatus, error) {
	return p.WaitTask(ctx, upid, 1000*time.Hour)
}
