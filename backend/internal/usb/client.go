// Package usb manages only explicitly selected external USB recording archives.
package usb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const Socket = "/run/dash-usb/control.sock"
const MountBase = "/data/dash/usb"

var uuidPattern = regexp.MustCompile(`^[A-Za-z0-9-]{4,64}$`)

type Volume struct {
	UUID       string `json:"uuid"`
	Label      string `json:"label"`
	Filesystem string `json:"filesystem"`
	Name       string `json:"name"`
	Mounted    bool   `json:"mounted"`
	Available  bool   `json:"available"`
}
type Client struct{ socket string }

func NewClient() *Client { return &Client{socket: Socket} }
func (c *Client) request(ctx context.Context, action, uuid string, target any) error {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", c.socket)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second}
	data, _ := json.Marshal(map[string]string{"uuid": uuid})
	req, _ := http.NewRequestWithContext(ctx, "POST", "http://dash-usb/"+action, bytes.NewReader(data))
	response, err := client.Do(req)
	if err != nil {
		return errors.New("USB helper unavailable; install the complete Dash service")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		var result struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&result)
		if result.Error != "" {
			return errors.New(result.Error)
		}
		return errors.New("USB operation failed")
	}
	if target != nil {
		return json.NewDecoder(io.LimitReader(response.Body, 65536)).Decode(target)
	}
	return nil
}
func (c *Client) Volumes(ctx context.Context) ([]Volume, error) {
	var volumes []Volume
	err := c.request(ctx, "list", "", &volumes)
	return volumes, err
}
func (c *Client) Mount(ctx context.Context, uuid string) (string, error) {
	if !uuidPattern.MatchString(uuid) {
		return "", errors.New("invalid USB volume UUID")
	}
	var result struct {
		Mounted bool `json:"mounted"`
	}
	if err := c.request(ctx, "mount", uuid, &result); err != nil {
		return "", err
	}
	if !result.Mounted {
		return "", errors.New("USB volume did not mount")
	}
	return MountBase + "/" + strings.ToLower(uuid), nil
}
func (c *Client) Eject(ctx context.Context, uuid string) error {
	if !uuidPattern.MatchString(uuid) {
		return errors.New("invalid USB volume UUID")
	}
	return c.request(ctx, "eject", uuid, &struct{}{})
}
