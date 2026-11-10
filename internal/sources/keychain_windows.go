// SPDX-License-Identifier: GPL-3.0-or-later

package sources

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows Credential Manager ("Anmeldeinformationsverwaltung"), generic
// credentials named "RepoShelf:<host>" (D-024).

var (
	advapi32       = windows.NewLazySystemDLL("advapi32.dll")
	procCredReadW  = advapi32.NewProc("CredReadW")
	procCredWriteW = advapi32.NewProc("CredWriteW")
	procCredDelete = advapi32.NewProc("CredDeleteW")
	procCredFree   = advapi32.NewProc("CredFree")
)

const (
	credTypeGeneric        = 1
	credPersistLocalMachin = 2
)

// credential mirrors CREDENTIALW.
type credential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        windows.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

// SystemKeychain returns the Windows Credential Manager.
func SystemKeychain() Keychain { return winCred{} }

type winCred struct{}

func target(host string) (*uint16, error) {
	return windows.UTF16PtrFromString(KeychainService + ":" + host)
}

func (winCred) Get(host string) (string, error) {
	name, err := target(host)
	if err != nil {
		return "", err
	}
	var c *credential
	r, _, e := procCredReadW.Call(uintptr(unsafe.Pointer(name)), credTypeGeneric, 0, uintptr(unsafe.Pointer(&c)))
	if r == 0 {
		if errors.Is(e, windows.ERROR_NOT_FOUND) {
			return "", nil
		}
		return "", e
	}
	defer func() { _, _, _ = procCredFree.Call(uintptr(unsafe.Pointer(c))) }()
	if c.CredentialBlobSize == 0 || c.CredentialBlob == nil {
		return "", nil
	}
	return string(unsafe.Slice(c.CredentialBlob, c.CredentialBlobSize)), nil
}

func (winCred) Set(host, token string) error {
	name, err := target(host)
	if err != nil {
		return err
	}
	user, err := windows.UTF16PtrFromString(host)
	if err != nil {
		return err
	}
	blob := []byte(token)
	if len(blob) == 0 {
		return ErrInvalidToken
	}
	c := credential{
		Type:               credTypeGeneric,
		TargetName:         name,
		CredentialBlobSize: uint32(len(blob)),
		CredentialBlob:     &blob[0],
		Persist:            credPersistLocalMachin,
		UserName:           user,
	}
	if r, _, e := procCredWriteW.Call(uintptr(unsafe.Pointer(&c)), 0); r == 0 {
		return e
	}
	return nil
}

func (winCred) Delete(host string) error {
	name, err := target(host)
	if err != nil {
		return err
	}
	if r, _, e := procCredDelete.Call(uintptr(unsafe.Pointer(name)), credTypeGeneric, 0); r == 0 && !errors.Is(e, windows.ERROR_NOT_FOUND) {
		return e
	}
	return nil
}
