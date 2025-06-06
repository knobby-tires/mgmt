// Mgmt
// Copyright (C) James Shubin and the project contributors
// Written by James Shubin <james@shubin.ca> and the project contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.
//
// Additional permission under GNU GPL version 3 section 7
//
// If you modify this program, or any covered work, by linking or combining it
// with embedded mcl code and modules (and that the embedded mcl code and
// modules which link with this program, contain a copy of their source code in
// the authoritative form) containing parts covered by the terms of any other
// license, the licensors of this program grant you additional permission to
// convey the resulting work. Furthermore, the licensors of this program grant
// the original author, James Shubin, additional permission to update this
// additional permission if he deems it necessary to achieve the goals of this
// additional permission.

package resources

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
	"os"
	"path"
	"strings"

	"github.com/purpleidea/mgmt/engine"
	"github.com/purpleidea/mgmt/engine/traits"
	engineUtil "github.com/purpleidea/mgmt/engine/util"
	"github.com/purpleidea/mgmt/util/errwrap"
	"github.com/purpleidea/mgmt/util/recwatch"
)

func init() {
	engine.RegisterResource("password", func() engine.Res { return &PasswordRes{} })
}

const (
	alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	newline  = "\n" // something not in alphabet that TrimSpace can trim
)

// PasswordRes is a no-op resource that returns a random password string.
type PasswordRes struct {
	traits.Base // add the base methods without re-implementation
	// TODO: it could be useful to group our tokens into a single write, and
	// as a result, we save inotify watches too!
	//traits.Groupable // TODO: this is doable, but probably not very useful
	traits.Refreshable
	traits.Sendable

	init *engine.Init

	// Length is the number of characters to return.
	// FIXME: is uint16 too big?
	Length uint16 `lang:"length" yaml:"length"`

	// Saved caches the password in the clear locally.
	Saved bool `lang:"saved" yaml:"saved"`

	// CheckRecovery specifies that we should recover from, regenerate, and
	// carry on casually without erroring the resource if the "check"
	// facility fails. This can happen when loading a saved password from
	// disk which is not of the expected length. In this case, we'd discard
	// the old saved password and create a new one without erroring.
	CheckRecovery bool `lang:"check_recovery" yaml:"check_recovery"`

	path       string // the path to local storage
	recWatcher *recwatch.RecWatcher
}

// Default returns some sensible defaults for this resource.
func (obj *PasswordRes) Default() engine.Res {
	return &PasswordRes{
		Length: 64, // safe default
	}
}

// Validate if the params passed in are valid data.
func (obj *PasswordRes) Validate() error {
	return nil
}

// Init runs some startup code for this resource. It generates a new password
// for this resource if one was not provided. It will save this into a local
// file. It will load it back in from previous runs.
func (obj *PasswordRes) Init(init *engine.Init) error {
	obj.init = init // save for later

	dir, err := obj.init.VarDir("")
	if err != nil {
		return errwrap.Wrapf(err, "could not get VarDir in Init()")
	}
	obj.path = path.Join(dir, "password") // return a unique file

	return nil
}

// Cleanup is run by the engine to clean up after the resource is done.
func (obj *PasswordRes) Cleanup() error {
	return nil
}

// read is a helper to read the data from disk. This is similar to an engineUtil
// function named ReadData but is kept separate for safety anyways.
func (obj *PasswordRes) read() (string, error) {
	file, err := os.Open(obj.path) // open a handle to read the file
	if err != nil {
		return "", err
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		return "", errwrap.Wrapf(err, "could not read from file")
	}
	return strings.TrimSpace(string(data)), nil
}

// write is a helper to store the data on disk. This is similar to an engineUtil
// function named WriteData but is kept separate for safety anyways.
func (obj *PasswordRes) write(password string) (int, error) {
	uid, gid, err := engineUtil.GetUIDGID()
	if err != nil {
		return -1, err
	}

	// Chmod it before we write the secret data.
	file, err := os.OpenFile(obj.path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return -1, errwrap.Wrapf(err, "can't create file")
	}
	defer file.Close()

	// Chown it before we write the secret data.
	if err := file.Chown(uid, gid); err != nil {
		return -1, err
	}

	c, err := file.Write([]byte(password + newline))
	if err != nil {
		return c, errwrap.Wrapf(err, "can't write file")
	}
	return c, file.Sync()
}

// generate generates a new password.
func (obj *PasswordRes) generate() (string, error) {
	max := len(alphabet) - 1 // last index
	output := ""

	// FIXME: have someone verify this is cryptographically secure & correct
	for i := uint16(0); i < obj.Length; i++ {
		big, err := rand.Int(rand.Reader, big.NewInt(int64(max)))
		if err != nil {
			return "", errwrap.Wrapf(err, "could not generate password")
		}
		ix := big.Int64()
		output += string(alphabet[ix])
	}

	if output == "" { // safety against empty passwords
		return "", fmt.Errorf("password is empty")
	}

	if uint16(len(output)) != obj.Length { // safety against weird bugs
		return "", fmt.Errorf("password length is too short") // bug!
	}

	return output, nil
}

// check validates a stored password string
func (obj *PasswordRes) check(value string) error {
	length := uint16(len(value))

	if !obj.Saved && length == 0 { // expecting an empty string
		return nil
	}
	if !obj.Saved && length != 0 { // should have no stored password
		return fmt.Errorf("expected empty token only")
	}

	if length != obj.Length {
		return fmt.Errorf("string length is not %d", obj.Length)
	}
Loop:
	for i := uint16(0); i < length; i++ {
		for j := 0; j < len(alphabet); j++ {
			if value[i] == alphabet[j] {
				continue Loop
			}
		}
		// we couldn't find that character, so error!
		return fmt.Errorf("invalid character `%s`", string(value[i]))
	}
	return nil
}

// Watch is the primary listener for this resource and it outputs events.
func (obj *PasswordRes) Watch(ctx context.Context) error {
	var err error
	obj.recWatcher, err = recwatch.NewRecWatcher(obj.path, false)
	if err != nil {
		return err
	}
	defer obj.recWatcher.Close()

	obj.init.Running() // when started, notify engine that we're running

	for {
		select {
		// NOTE: this part is very similar to the file resource code
		case event, ok := <-obj.recWatcher.Events():
			if !ok { // channel shutdown
				return nil
			}
			if err := event.Error; err != nil {
				return errwrap.Wrapf(err, "unknown %s watcher error", obj)
			}

		case <-ctx.Done(): // closed by the engine to signal shutdown
			return nil
		}

		obj.init.Event() // notify engine of an event (this can block)
	}
}

// CheckApply method for Password resource. Does nothing, returns happy!
func (obj *PasswordRes) CheckApply(ctx context.Context, apply bool) (bool, error) {
	var refresh = obj.init.Refresh() // do we have a pending reload to apply?
	var exists = true                // does the file (aka the token) exist?
	var generate bool                // do we need to generate a new password?
	var write bool                   // do we need to write out to disk?

	password, err := obj.read() // password might be empty if just a token
	if err != nil {
		if !os.IsNotExist(err) {
			return false, errwrap.Wrapf(err, "unknown read error")
		}
		exists = false
	}

	if exists {
		if err := obj.check(password); err != nil {
			if !obj.CheckRecovery {
				return false, errwrap.Wrapf(err, "check failed")
			}
			obj.init.Logf("integrity check failed")
			generate = true // okay to build a new one
			write = true    // make sure to write over the old one
		}
	} else { // doesn't exist, write one
		write = true
	}

	// if we previously had !obj.Saved, and now we want it, we re-generate!
	if refresh || !exists || (obj.Saved && password == "") {
		generate = true
	}

	// stored password isn't consistent with memory
	//if p := obj.Password; obj.Saved && (p != nil && *p != password) {
	//	write = true
	//}

	if !refresh && exists && !generate && !write { // nothing to do, done!
		if err := obj.init.Send(&PasswordSends{
			Password: &password,
		}); err != nil {
			return false, err
		}
		return true, nil
	}
	// a refresh was requested, the token doesn't exist, or the check failed

	if !apply {
		if err := obj.init.Send(&PasswordSends{
			Password: &password, // XXX: arbitrary since we're in noop mode
		}); err != nil {
			return false, err
		}
		return false, nil
	}

	if generate {
		// we'll need to write this out...
		if obj.Saved || (!obj.Saved && password != "") {
			write = true
		}
		// generate the actual password
		var err error
		obj.init.Logf("generating new password...")
		if password, err = obj.generate(); err != nil { // generate one!
			return false, errwrap.Wrapf(err, "could not generate password")
		}
	}

	// send
	if err := obj.init.Send(&PasswordSends{
		Password: &password,
	}); err != nil {
		return false, err
	}

	var output string // the string to write out

	// if memory value != value on disk, save it
	if write {
		if obj.Saved { // save password as clear text
			// TODO: would it make sense to encrypt this password?
			output = password
		}
		// write either an empty token, or the password
		obj.init.Logf("writing password token...")
		if _, err := obj.write(output); err != nil {
			return false, errwrap.Wrapf(err, "can't write to file")
		}
	}

	return false, nil
}

// Cmp compares two resources and returns an error if they are not equivalent.
func (obj *PasswordRes) Cmp(r engine.Res) error {
	// we can only compare PasswordRes to others of the same resource kind
	res, ok := r.(*PasswordRes)
	if !ok {
		return fmt.Errorf("not a %s", obj.Kind())
	}

	if obj.Length != res.Length {
		return fmt.Errorf("the Length differs")
	}
	// TODO: we *could* optimize by allowing CheckApply to move from
	// saved->!saved, by removing the file, but not likely worth it!
	if obj.Saved != res.Saved {
		return fmt.Errorf("the Saved differs")
	}
	if obj.CheckRecovery != res.CheckRecovery {
		return fmt.Errorf("the CheckRecovery differs")
	}

	return nil
}

// PasswordUID is the UID struct for PasswordRes.
type PasswordUID struct {
	engine.BaseUID
	name string
}

// UIDs includes all params to make a unique identification of this object. Most
// resources only return one, although some resources can return multiple.
func (obj *PasswordRes) UIDs() []engine.ResUID {
	x := &PasswordUID{
		BaseUID: engine.BaseUID{Name: obj.Name(), Kind: obj.Kind()},
		name:    obj.Name(),
	}
	return []engine.ResUID{x}
}

// PasswordSends is the struct of data which is sent after a successful Apply.
type PasswordSends struct {
	// Password is the generated password being sent.
	Password *string `lang:"password"`
	// Hashing is the algorithm used for this password. Empty is plain text.
	Hashing string // TODO: implement me
}

// Sends represents the default struct of values we can send using Send/Recv.
func (obj *PasswordRes) Sends() interface{} {
	return &PasswordSends{
		Password: nil,
	}
}

// UnmarshalYAML is the custom unmarshal handler for this struct. It is
// primarily useful for setting the defaults.
func (obj *PasswordRes) UnmarshalYAML(unmarshal func(interface{}) error) error {
	type rawRes PasswordRes // indirection to avoid infinite recursion

	def := obj.Default()          // get the default
	res, ok := def.(*PasswordRes) // put in the right format
	if !ok {
		return fmt.Errorf("could not convert to PasswordRes")
	}
	raw := rawRes(*res) // convert; the defaults go here

	if err := unmarshal(&raw); err != nil {
		return err
	}

	*obj = PasswordRes(raw) // restore from indirection with type conversion!
	return nil
}
