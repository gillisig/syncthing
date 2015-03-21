// Copyright (C) 2014 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at http://mozilla.org/MPL/2.0/.

// Package config implements reading and writing of the syncthing configuration file.
package config

import (
	"encoding/xml"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/calmh/logger"
	"github.com/syncthing/protocol"
	"github.com/syncthing/syncthing/internal/osutil"
	"golang.org/x/crypto/bcrypt"
)

var l = logger.DefaultLogger

const CurrentVersion = 10

type Configuration struct {
	Version        int                   `xml:"version,attr" json:"version"`
	Folders        []FolderConfiguration `xml:"folder" json:"folders"`
	Devices        []DeviceConfiguration `xml:"device" json:"devices"`
	GUI            GUIConfiguration      `xml:"gui" json:"gui"`
	Options        OptionsConfiguration  `xml:"options" json:"options"`
	IgnoredDevices []protocol.DeviceID   `xml:"ignoredDevice" json:"ignoredDevices"`
	XMLName        xml.Name              `xml:"configuration" json:"-"`

	OriginalVersion         int                   `xml:"-" json:"-"` // The version we read from disk, before any conversion
	Deprecated_Repositories []FolderConfiguration `xml:"repository" json:"-"`
	Deprecated_Nodes        []DeviceConfiguration `xml:"node" json:"-"`
}

type FolderConfiguration struct {
	ID              string                      `xml:"id,attr" json:"id"`
	Path            string                      `xml:"path,attr" json:"path"`
	Devices         []FolderDeviceConfiguration `xml:"device" json:"devices"`
	ReadOnly        bool                        `xml:"ro,attr" json:"readOnly"`
	RescanIntervalS int                         `xml:"rescanIntervalS,attr" json:"rescanIntervalS"`
	IgnorePerms     bool                        `xml:"ignorePerms,attr" json:"ignorePerms"`
	AutoNormalize   bool                        `xml:"autoNormalize,attr" json:"autoNormalize"`
	Versioning      VersioningConfiguration     `xml:"versioning" json:"versioning"`
	LenientMtimes   bool                        `xml:"lenientMtimes" json:"lenientMTimes"`
	Copiers         int                         `xml:"copiers" json:"copiers"` // This defines how many files are handled concurrently.
	Pullers         int                         `xml:"pullers" json:"pullers"` // Defines how many blocks are fetched at the same time, possibly between separate copier routines.
	Hashers         int                         `xml:"hashers" json:"hashers"` // Less than one sets the value to the number of cores. These are CPU bound due to hashing.

	Invalid string `xml:"-" json:"invalid"` // Set at runtime when there is an error, not saved

	deviceIDs []protocol.DeviceID

	Deprecated_Directory string                      `xml:"directory,omitempty,attr" json:"-"`
	Deprecated_Nodes     []FolderDeviceConfiguration `xml:"node" json:"-"`
}

func (f *FolderConfiguration) CreateMarker() error {
	if !f.HasMarker() {
		marker := filepath.Join(f.Path, ".stfolder")
		fd, err := os.Create(marker)
		if err != nil {
			return err
		}
		fd.Close()
		osutil.HideFile(marker)
	}

	return nil
}

func (f *FolderConfiguration) HasMarker() bool {
	_, err := os.Stat(filepath.Join(f.Path, ".stfolder"))
	if err != nil {
		return false
	}
	return true
}

func (f *FolderConfiguration) DeviceIDs() []protocol.DeviceID {
	if f.deviceIDs == nil {
		for _, n := range f.Devices {
			f.deviceIDs = append(f.deviceIDs, n.DeviceID)
		}
	}
	return f.deviceIDs
}

type VersioningConfiguration struct {
	Type   string            `xml:"type,attr" json:"type"`
	Params map[string]string `json:"params"`
}

type InternalVersioningConfiguration struct {
	Type   string          `xml:"type,attr,omitempty"`
	Params []InternalParam `xml:"param"`
}

type InternalParam struct {
	Key string `xml:"key,attr"`
	Val string `xml:"val,attr"`
}

func (c *VersioningConfiguration) MarshalXML(e *xml.Encoder, start xml.StartElement) error {
	var tmp InternalVersioningConfiguration
	tmp.Type = c.Type
	for k, v := range c.Params {
		tmp.Params = append(tmp.Params, InternalParam{k, v})
	}

	return e.EncodeElement(tmp, start)

}

func (c *VersioningConfiguration) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	var tmp InternalVersioningConfiguration
	err := d.DecodeElement(&tmp, &start)
	if err != nil {
		return err
	}

	c.Type = tmp.Type
	c.Params = make(map[string]string, len(tmp.Params))
	for _, p := range tmp.Params {
		c.Params[p.Key] = p.Val
	}
	return nil
}

type DeviceConfiguration struct {
	DeviceID    protocol.DeviceID    `xml:"id,attr" json:"deviceID"`
	Name        string               `xml:"name,attr,omitempty" json:"name"`
	Addresses   []string             `xml:"address,omitempty" json:"addresses"`
	Compression protocol.Compression `xml:"compression,attr" json:"compression"`
	CertName    string               `xml:"certName,attr,omitempty" json:"certName"`
	Introducer  bool                 `xml:"introducer,attr" json:"introducer"`
}

type FolderDeviceConfiguration struct {
	DeviceID protocol.DeviceID `xml:"id,attr" json:"deviceID"`

	Deprecated_Name      string   `xml:"name,attr,omitempty" json:"-"`
	Deprecated_Addresses []string `xml:"address,omitempty" json:"-"`
}

type OptionsConfiguration struct {
	ListenAddress           []string `xml:"listenAddress" json:"listenAddress" default:"0.0.0.0:22000"`
	GlobalAnnServers        []string `xml:"globalAnnounceServer" json:"globalAnnounceServers" json:"globalAnnounceServer" default:"udp4://announce.syncthing.net:22026, udp6://announce-v6.syncthing.net:22026"`
	GlobalAnnEnabled        bool     `xml:"globalAnnounceEnabled" json:"globalAnnounceEnabled" default:"true"`
	LocalAnnEnabled         bool     `xml:"localAnnounceEnabled" json:"localAnnounceEnabled" default:"true"`
	LocalAnnPort            int      `xml:"localAnnouncePort" json:"localAnnouncePort" default:"21025"`
	LocalAnnMCAddr          string   `xml:"localAnnounceMCAddr" json:"localAnnounceMCAddr" default:"[ff32::5222]:21026"`
	MaxSendKbps             int      `xml:"maxSendKbps" json:"maxSendKbps"`
	MaxRecvKbps             int      `xml:"maxRecvKbps" json:"maxRecvKbps"`
	ReconnectIntervalS      int      `xml:"reconnectionIntervalS" json:"reconnectionIntervalS" default:"60"`
	StartBrowser            bool     `xml:"startBrowser" json:"startBrowser" default:"true"`
	UPnPEnabled             bool     `xml:"upnpEnabled" json:"upnpEnabled" default:"true"`
	UPnPLease               int      `xml:"upnpLeaseMinutes" json:"upnpLeaseMinutes" default:"0"`
	UPnPRenewal             int      `xml:"upnpRenewalMinutes" json:"upnpRenewalMinutes" default:"30"`
	URAccepted              int      `xml:"urAccepted" json:"urAccepted"` // Accepted usage reporting version; 0 for off (undecided), -1 for off (permanently)
	URUniqueID              string   `xml:"urUniqueID" json:"urUniqueId"` // Unique ID for reporting purposes, regenerated when UR is turned on.
	RestartOnWakeup         bool     `xml:"restartOnWakeup" json:"restartOnWakeup" default:"true"`
	AutoUpgradeIntervalH    int      `xml:"autoUpgradeIntervalH" json:"autoUpgradeIntervalH" default:"12"` // 0 for off
	KeepTemporariesH        int      `xml:"keepTemporariesH" json:"keepTemporariesH" default:"24"`         // 0 for off
	CacheIgnoredFiles       bool     `xml:"cacheIgnoredFiles" json:"cacheIgnoredFiles" default:"true"`
	ProgressUpdateIntervalS int      `xml:"progressUpdateIntervalS" json:"progressUpdateIntervalS" default:"5"`
	SymlinksEnabled         bool     `xml:"symlinksEnabled" json:"symlinksEnabled" default:"true"`
	LimitBandwidthInLan     bool     `xml:"limitBandwidthInLan" json:"limitBandwidthInLan" default:"false"`

	Deprecated_RescanIntervalS int    `xml:"rescanIntervalS,omitempty" json:"-"`
	Deprecated_UREnabled       bool   `xml:"urEnabled,omitempty" json:"-"`
	Deprecated_URDeclined      bool   `xml:"urDeclined,omitempty" json:"-"`
	Deprecated_ReadOnly        bool   `xml:"readOnly,omitempty" json:"-"`
	Deprecated_GUIEnabled      bool   `xml:"guiEnabled,omitempty" json:"-"`
	Deprecated_GUIAddress      string `xml:"guiAddress,omitempty" json:"-"`
}

type GUIConfiguration struct {
	Enabled  bool   `xml:"enabled,attr" json:"enabled" default:"true"`
	Address  string `xml:"address" json:"address" default:"127.0.0.1:8080"`
	User     string `xml:"user,omitempty" json:"user"`
	Password string `xml:"password,omitempty" json:"password"`
	UseTLS   bool   `xml:"tls,attr" json:"useTLS"`
	APIKey   string `xml:"apikey,omitempty" json:"apiKey"`
}

func New(myID protocol.DeviceID) Configuration {
	var cfg Configuration
	cfg.Version = CurrentVersion
	cfg.OriginalVersion = CurrentVersion

	setDefaults(&cfg)
	setDefaults(&cfg.Options)
	setDefaults(&cfg.GUI)

	cfg.prepare(myID)

	return cfg
}

func ReadXML(r io.Reader, myID protocol.DeviceID) (Configuration, error) {
	var cfg Configuration

	setDefaults(&cfg)
	setDefaults(&cfg.Options)
	setDefaults(&cfg.GUI)

	err := xml.NewDecoder(r).Decode(&cfg)
	cfg.OriginalVersion = cfg.Version

	cfg.prepare(myID)
	return cfg, err
}

func (cfg *Configuration) WriteXML(w io.Writer) error {
	e := xml.NewEncoder(w)
	e.Indent("", "    ")
	err := e.Encode(cfg)
	if err != nil {
		return err
	}
	_, err = w.Write([]byte("\n"))
	return err
}

func (cfg *Configuration) prepare(myID protocol.DeviceID) {
	fillNilSlices(&cfg.Options)

	// Initialize an empty slices
	if cfg.Folders == nil {
		cfg.Folders = []FolderConfiguration{}
	}
	if cfg.IgnoredDevices == nil {
		cfg.IgnoredDevices = []protocol.DeviceID{}
	}

	// Check for missing, bad or duplicate folder ID:s
	var seenFolders = map[string]*FolderConfiguration{}
	var uniqueCounter int
	for i := range cfg.Folders {
		folder := &cfg.Folders[i]

		if len(folder.Path) == 0 {
			folder.Invalid = "no directory configured"
			continue
		}

		// The reason it's done like this:
		// C:          ->  C:\            ->  C:\        (issue that this is trying to fix)
		// C:\somedir  ->  C:\somedir\    ->  C:\somedir
		// C:\somedir\ ->  C:\somedir\\   ->  C:\somedir
		// This way in the tests, we get away without OS specific separators
		// in the test configs.
		folder.Path = filepath.Dir(folder.Path + string(filepath.Separator))

		if folder.ID == "" {
			folder.ID = "default"
		}

		if seen, ok := seenFolders[folder.ID]; ok {
			l.Warnf("Multiple folders with ID %q; disabling", folder.ID)

			seen.Invalid = "duplicate folder ID"
			if seen.ID == folder.ID {
				uniqueCounter++
				seen.ID = fmt.Sprintf("%s~%d", folder.ID, uniqueCounter)
			}
			folder.Invalid = "duplicate folder ID"
			uniqueCounter++
			folder.ID = fmt.Sprintf("%s~%d", folder.ID, uniqueCounter)
		} else {
			seenFolders[folder.ID] = folder
		}
	}

	if cfg.Options.Deprecated_URDeclined {
		cfg.Options.URAccepted = -1
		cfg.Options.URUniqueID = ""
	}
	cfg.Options.Deprecated_URDeclined = false
	cfg.Options.Deprecated_UREnabled = false

	// Upgrade configuration versions as appropriate
	if cfg.Version == 1 {
		convertV1V2(cfg)
	}
	if cfg.Version == 2 {
		convertV2V3(cfg)
	}
	if cfg.Version == 3 {
		convertV3V4(cfg)
	}
	if cfg.Version == 4 {
		convertV4V5(cfg)
	}
	if cfg.Version == 5 {
		convertV5V6(cfg)
	}
	if cfg.Version == 6 {
		convertV6V7(cfg)
	}
	if cfg.Version == 7 {
		convertV7V8(cfg)
	}
	if cfg.Version == 8 {
		convertV8V9(cfg)
	}
	if cfg.Version == 9 {
		convertV9V10(cfg)
	}

	// Hash old cleartext passwords
	if len(cfg.GUI.Password) > 0 && cfg.GUI.Password[0] != '$' {
		hash, err := bcrypt.GenerateFromPassword([]byte(cfg.GUI.Password), 0)
		if err != nil {
			l.Warnln("bcrypting password:", err)
		} else {
			cfg.GUI.Password = string(hash)
		}
	}

	// Build a list of available devices
	existingDevices := make(map[protocol.DeviceID]bool)
	for _, device := range cfg.Devices {
		existingDevices[device.DeviceID] = true
	}

	// Ensure this device is present in the config
	if !existingDevices[myID] {
		myName, _ := os.Hostname()
		cfg.Devices = append(cfg.Devices, DeviceConfiguration{
			DeviceID: myID,
			Name:     myName,
		})
		existingDevices[myID] = true
	}

	sort.Sort(DeviceConfigurationList(cfg.Devices))
	// Ensure that any loose devices are not present in the wrong places
	// Ensure that there are no duplicate devices
	// Ensure that puller settings are sane
	for i := range cfg.Folders {
		cfg.Folders[i].Devices = ensureDevicePresent(cfg.Folders[i].Devices, myID)
		cfg.Folders[i].Devices = ensureExistingDevices(cfg.Folders[i].Devices, existingDevices)
		cfg.Folders[i].Devices = ensureNoDuplicates(cfg.Folders[i].Devices)
		if cfg.Folders[i].Copiers == 0 {
			cfg.Folders[i].Copiers = 1
		}
		if cfg.Folders[i].Pullers == 0 {
			cfg.Folders[i].Pullers = 16
		}
		sort.Sort(FolderDeviceConfigurationList(cfg.Folders[i].Devices))
	}

	// An empty address list is equivalent to a single "dynamic" entry
	for i := range cfg.Devices {
		n := &cfg.Devices[i]
		if len(n.Addresses) == 0 || len(n.Addresses) == 1 && n.Addresses[0] == "" {
			n.Addresses = []string{"dynamic"}
		}
	}

	cfg.Options.ListenAddress = uniqueStrings(cfg.Options.ListenAddress)
	cfg.Options.GlobalAnnServers = uniqueStrings(cfg.Options.GlobalAnnServers)

	if cfg.GUI.APIKey == "" {
		cfg.GUI.APIKey = randomString(32)
	}
}

// ChangeRequiresRestart returns true if updating the configuration requires a
// complete restart.
func ChangeRequiresRestart(from, to Configuration) bool {
	// Adding, removing or changing folders requires restart
	if !reflect.DeepEqual(from.Folders, to.Folders) {
		return true
	}

	// Removing a device requres restart
	toDevs := make(map[protocol.DeviceID]bool, len(from.Devices))
	for _, dev := range to.Devices {
		toDevs[dev.DeviceID] = true
	}
	for _, dev := range from.Devices {
		if _, ok := toDevs[dev.DeviceID]; !ok {
			return true
		}
	}

	// Changing usage reporting to on or off does not require a restart.
	to.Options.URAccepted = from.Options.URAccepted
	to.Options.URUniqueID = from.Options.URUniqueID

	// All of the generic options require restart
	if !reflect.DeepEqual(from.Options, to.Options) || !reflect.DeepEqual(from.GUI, to.GUI) {
		return true
	}

	return false
}

func convertV9V10(cfg *Configuration) {
	// Enable auto normalization on existing folders.
	for i := range cfg.Folders {
		cfg.Folders[i].AutoNormalize = true
	}
	cfg.Version = 10
}

func convertV8V9(cfg *Configuration) {
	// Compression is interpreted and serialized differently, but no enforced
	// changes. Still need a new version number since the compression stuff
	// isn't understandable by earlier versions.
	cfg.Version = 9
}

func convertV7V8(cfg *Configuration) {
	// Add IPv6 announce server
	if len(cfg.Options.GlobalAnnServers) == 1 && cfg.Options.GlobalAnnServers[0] == "udp4://announce.syncthing.net:22026" {
		cfg.Options.GlobalAnnServers = append(cfg.Options.GlobalAnnServers, "udp6://announce-v6.syncthing.net:22026")
	}

	cfg.Version = 8
}

func convertV6V7(cfg *Configuration) {
	// Migrate announce server addresses to the new URL based format
	for i := range cfg.Options.GlobalAnnServers {
		cfg.Options.GlobalAnnServers[i] = "udp4://" + cfg.Options.GlobalAnnServers[i]
	}

	cfg.Version = 7
}

func convertV5V6(cfg *Configuration) {
	// Added ".stfolder" file at folder roots to identify mount issues
	// Doesn't affect the config itself, but uses config migrations to identify
	// the migration point.
	for _, folder := range Wrap("", *cfg).Folders() {
		// Best attempt, if it fails, it fails, the user will have to fix
		// it up manually, as the repo will not get started.
		folder.CreateMarker()
	}

	cfg.Version = 6
}

func convertV4V5(cfg *Configuration) {
	// Renamed a bunch of fields in the structs.
	if cfg.Deprecated_Nodes == nil {
		cfg.Deprecated_Nodes = []DeviceConfiguration{}
	}

	if cfg.Deprecated_Repositories == nil {
		cfg.Deprecated_Repositories = []FolderConfiguration{}
	}

	cfg.Devices = cfg.Deprecated_Nodes
	cfg.Folders = cfg.Deprecated_Repositories

	for i := range cfg.Folders {
		cfg.Folders[i].Path = cfg.Folders[i].Deprecated_Directory
		cfg.Folders[i].Deprecated_Directory = ""
		cfg.Folders[i].Devices = cfg.Folders[i].Deprecated_Nodes
		cfg.Folders[i].Deprecated_Nodes = nil
	}

	cfg.Deprecated_Nodes = nil
	cfg.Deprecated_Repositories = nil

	cfg.Version = 5
}

func convertV3V4(cfg *Configuration) {
	// In previous versions, rescan interval was common for each folder.
	// From now, it can be set independently. We have to make sure, that after upgrade
	// the individual rescan interval will be defined for every existing folder.
	for i := range cfg.Deprecated_Repositories {
		cfg.Deprecated_Repositories[i].RescanIntervalS = cfg.Options.Deprecated_RescanIntervalS
	}

	cfg.Options.Deprecated_RescanIntervalS = 0

	// In previous versions, folders held full device configurations.
	// Since that's the only place where device configs were in V1, we still have
	// to define the deprecated fields to be able to upgrade from V1 to V4.
	for i, folder := range cfg.Deprecated_Repositories {

		for j := range folder.Deprecated_Nodes {
			rncfg := cfg.Deprecated_Repositories[i].Deprecated_Nodes[j]
			rncfg.Deprecated_Name = ""
			rncfg.Deprecated_Addresses = nil
		}
	}

	cfg.Version = 4
}

func convertV2V3(cfg *Configuration) {
	// In previous versions, compression was always on. When upgrading, enable
	// compression on all existing new. New devices will get compression on by
	// default by the GUI.
	for i := range cfg.Deprecated_Nodes {
		cfg.Deprecated_Nodes[i].Compression = protocol.CompressMetadata
	}

	// The global discovery format and port number changed in v0.9. Having the
	// default announce server but old port number is guaranteed to be legacy.
	if len(cfg.Options.GlobalAnnServers) == 1 && cfg.Options.GlobalAnnServers[0] == "announce.syncthing.net:22025" {
		cfg.Options.GlobalAnnServers = []string{"announce.syncthing.net:22026"}
	}

	cfg.Version = 3
}

func convertV1V2(cfg *Configuration) {
	// Collect the list of devices.
	// Replace device configs inside folders with only a reference to the
	// device ID. Set all folders to read only if the global read only flag is
	// set.
	var devices = map[string]FolderDeviceConfiguration{}
	for i, folder := range cfg.Deprecated_Repositories {
		cfg.Deprecated_Repositories[i].ReadOnly = cfg.Options.Deprecated_ReadOnly
		for j, device := range folder.Deprecated_Nodes {
			id := device.DeviceID.String()
			if _, ok := devices[id]; !ok {
				devices[id] = device
			}
			cfg.Deprecated_Repositories[i].Deprecated_Nodes[j] = FolderDeviceConfiguration{DeviceID: device.DeviceID}
		}
	}
	cfg.Options.Deprecated_ReadOnly = false

	// Set and sort the list of devices.
	for _, device := range devices {
		cfg.Deprecated_Nodes = append(cfg.Deprecated_Nodes, DeviceConfiguration{
			DeviceID:  device.DeviceID,
			Name:      device.Deprecated_Name,
			Addresses: device.Deprecated_Addresses,
		})
	}
	sort.Sort(DeviceConfigurationList(cfg.Deprecated_Nodes))

	// GUI
	cfg.GUI.Address = cfg.Options.Deprecated_GUIAddress
	cfg.GUI.Enabled = cfg.Options.Deprecated_GUIEnabled
	cfg.Options.Deprecated_GUIEnabled = false
	cfg.Options.Deprecated_GUIAddress = ""

	cfg.Version = 2
}

func setDefaults(data interface{}) error {
	s := reflect.ValueOf(data).Elem()
	t := s.Type()

	for i := 0; i < s.NumField(); i++ {
		f := s.Field(i)
		tag := t.Field(i).Tag

		v := tag.Get("default")
		if len(v) > 0 {
			switch f.Interface().(type) {
			case string:
				f.SetString(v)

			case int:
				i, err := strconv.ParseInt(v, 10, 64)
				if err != nil {
					return err
				}
				f.SetInt(i)

			case bool:
				f.SetBool(v == "true")

			case []string:
				// We don't do anything with string slices here. Any default
				// we set will be appended to by the XML decoder, so we fill
				// those after decoding.

			default:
				panic(f.Type())
			}
		}
	}
	return nil
}

// fillNilSlices sets default value on slices that are still nil.
func fillNilSlices(data interface{}) error {
	s := reflect.ValueOf(data).Elem()
	t := s.Type()

	for i := 0; i < s.NumField(); i++ {
		f := s.Field(i)
		tag := t.Field(i).Tag

		v := tag.Get("default")
		if len(v) > 0 {
			switch f.Interface().(type) {
			case []string:
				if f.IsNil() {
					// Treat the default as a comma separated slice
					vs := strings.Split(v, ",")
					for i := range vs {
						vs[i] = strings.TrimSpace(vs[i])
					}

					rv := reflect.MakeSlice(reflect.TypeOf([]string{}), len(vs), len(vs))
					for i, v := range vs {
						rv.Index(i).SetString(v)
					}
					f.Set(rv)
				}
			}
		}
	}
	return nil
}

func uniqueStrings(ss []string) []string {
	var m = make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}

	var us = make([]string, 0, len(m))
	for k := range m {
		us = append(us, k)
	}

	sort.Strings(us)

	return us
}

func ensureDevicePresent(devices []FolderDeviceConfiguration, myID protocol.DeviceID) []FolderDeviceConfiguration {
	for _, device := range devices {
		if device.DeviceID.Equals(myID) {
			return devices
		}
	}

	devices = append(devices, FolderDeviceConfiguration{
		DeviceID: myID,
	})

	return devices
}

func ensureExistingDevices(devices []FolderDeviceConfiguration, existingDevices map[protocol.DeviceID]bool) []FolderDeviceConfiguration {
	count := len(devices)
	i := 0
loop:
	for i < count {
		if _, ok := existingDevices[devices[i].DeviceID]; !ok {
			devices[i] = devices[count-1]
			count--
			continue loop
		}
		i++
	}
	return devices[0:count]
}

func ensureNoDuplicates(devices []FolderDeviceConfiguration) []FolderDeviceConfiguration {
	count := len(devices)
	i := 0
	seenDevices := make(map[protocol.DeviceID]bool)
loop:
	for i < count {
		id := devices[i].DeviceID
		if _, ok := seenDevices[id]; ok {
			devices[i] = devices[count-1]
			count--
			continue loop
		}
		seenDevices[id] = true
		i++
	}
	return devices[0:count]
}

type DeviceConfigurationList []DeviceConfiguration

func (l DeviceConfigurationList) Less(a, b int) bool {
	return l[a].DeviceID.Compare(l[b].DeviceID) == -1
}
func (l DeviceConfigurationList) Swap(a, b int) {
	l[a], l[b] = l[b], l[a]
}
func (l DeviceConfigurationList) Len() int {
	return len(l)
}

type FolderDeviceConfigurationList []FolderDeviceConfiguration

func (l FolderDeviceConfigurationList) Less(a, b int) bool {
	return l[a].DeviceID.Compare(l[b].DeviceID) == -1
}
func (l FolderDeviceConfigurationList) Swap(a, b int) {
	l[a], l[b] = l[b], l[a]
}
func (l FolderDeviceConfigurationList) Len() int {
	return len(l)
}

// randomCharset contains the characters that can make up a randomString().
const randomCharset = "01234567890abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ-"

// randomString returns a string of random characters (taken from
// randomCharset) of the specified length.
func randomString(l int) string {
	bs := make([]byte, l)
	for i := range bs {
		bs[i] = randomCharset[rand.Intn(len(randomCharset))]
	}
	return string(bs)
}
