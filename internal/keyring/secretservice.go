// Package keyring reads secrets from the freedesktop Secret Service (GNOME Keyring / KWallet)
// over D-Bus. It is read-only and only used to pull credentials out of other apps' stores.
package keyring

import (
	"errors"
	"fmt"

	"github.com/godbus/dbus/v5"
)

const (
	dest     = "org.freedesktop.secrets"
	svcPath  = dbus.ObjectPath("/org/freedesktop/secrets")
	svcIface = "org.freedesktop.Secret.Service"
)

type secret struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

type Client struct {
	conn    *dbus.Conn
	session dbus.ObjectPath
}

// Open connects to the session bus and opens a plain (unencrypted, local-only) secret session.
func Open() (*Client, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, fmt.Errorf("session bus: %w", err)
	}
	var out dbus.Variant
	var session dbus.ObjectPath
	err = conn.Object(dest, svcPath).Call(svcIface+".OpenSession", 0, "plain", dbus.MakeVariant("")).Store(&out, &session)
	if err != nil {
		return nil, fmt.Errorf("secret service: %w", err)
	}
	return &Client{conn: conn, session: session}, nil
}

func (c *Client) Close() {
	if c == nil {
		return
	}
	_ = c.conn.Object(dest, c.session).Call("org.freedesktop.Secret.Session.Close", 0).Err
}

// Lookup returns the secret of the first item whose attributes match attrs.
// ok is false when nothing matches; an error means the store could not be read (e.g. locked).
func (c *Client) Lookup(attrs map[string]string) (value string, ok bool, err error) {
	var unlocked, locked []dbus.ObjectPath
	if err := c.conn.Object(dest, svcPath).Call(svcIface+".SearchItems", 0, attrs).Store(&unlocked, &locked); err != nil {
		return "", false, err
	}
	if len(unlocked) == 0 {
		if len(locked) == 0 {
			return "", false, nil
		}
		return "", false, errors.New("keyring is locked")
	}
	var secrets map[dbus.ObjectPath]secret
	if err := c.conn.Object(dest, svcPath).Call(svcIface+".GetSecrets", 0, unlocked[:1], c.session).Store(&secrets); err != nil {
		return "", false, err
	}
	for _, s := range secrets {
		return string(s.Value), true, nil
	}
	return "", false, nil
}

// LookupService is Lookup keyed by the "service" attribute, which is how the IntelliJ
// platform credential store labels its entries.
func (c *Client) LookupService(service string) (string, bool, error) {
	return c.Lookup(map[string]string{"service": service})
}
