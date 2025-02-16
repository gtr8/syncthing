// Copyright (C) 2021 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

// Package generate implements the `syncthing generate` subcommand.
package generate

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"os"

	"github.com/syncthing/syncthing/cmd/syncthing/cmdutil"
	"github.com/syncthing/syncthing/lib/config"
	"github.com/syncthing/syncthing/lib/events"
	"github.com/syncthing/syncthing/lib/fs"
	"github.com/syncthing/syncthing/lib/locations"
	"github.com/syncthing/syncthing/lib/osutil"
	"github.com/syncthing/syncthing/lib/protocol"
	"github.com/syncthing/syncthing/lib/syncthing"
)

type CLI struct {
	cmdutil.CommonOptions
	GUIUser     string `placeholder:"STRING" help:"Specify new GUI authentication user name"`
	GUIPassword string `placeholder:"STRING" help:"Specify new GUI authentication password (use - to read from standard input)"`
}

func (c *CLI) Run() error {
	log.SetFlags(0)

	if c.HideConsole {
		osutil.HideConsole()
	}

	if c.HomeDir != "" {
		if c.ConfDir != "" {
			return fmt.Errorf("--home must not be used together with --config")
		}
		c.ConfDir = c.HomeDir
	}
	if c.ConfDir == "" {
		c.ConfDir = locations.GetBaseDir(locations.ConfigBaseDir)
	}

	// Support reading the password from a pipe or similar
	if c.GUIPassword == "-" {
		reader := bufio.NewReader(os.Stdin)
		password, _, err := reader.ReadLine()
		if err != nil {
			return fmt.Errorf("failed reading GUI password: %w", err)
		}
		c.GUIPassword = string(password)
	}

	if err := Generate(c.ConfDir, c.GUIUser, c.GUIPassword, c.NoDefaultFolder, c.SkipPortProbing); err != nil {
		return fmt.Errorf("failed to generate config and keys: %w", err)
	}
	return nil
}

func Generate(confDir, guiUser, guiPassword string, noDefaultFolder, skipPortProbing bool) error {
	dir, err := fs.ExpandTilde(confDir)
	if err != nil {
		return err
	}

	if err := syncthing.EnsureDir(dir, 0700); err != nil {
		return err
	}
	locations.SetBaseDir(locations.ConfigBaseDir, dir)

	var myID protocol.DeviceID
	certFile, keyFile := locations.Get(locations.CertFile), locations.Get(locations.KeyFile)
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err == nil {
		log.Println("WARNING: Key exists; will not overwrite.")
	} else {
		cert, err = syncthing.GenerateCertificate(certFile, keyFile)
		if err != nil {
			return fmt.Errorf("create certificate: %w", err)
		}
	}
	myID = protocol.NewDeviceID(cert.Certificate[0])
	log.Println("Device ID:", myID)

	cfgFile := locations.Get(locations.ConfigFile)
	cfg, _, err := config.Load(cfgFile, myID, events.NoopLogger)
	if fs.IsNotExist(err) {
		if cfg, err = syncthing.DefaultConfig(cfgFile, myID, events.NoopLogger, noDefaultFolder, skipPortProbing); err != nil {
			return fmt.Errorf("create config: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go cfg.Serve(ctx)
	defer cancel()

	var updateErr error
	waiter, err := cfg.Modify(func(cfg *config.Configuration) {
		updateErr = updateGUIAuthentication(&cfg.GUI, guiUser, guiPassword)
	})
	if err != nil {
		return fmt.Errorf("modify config: %w", err)
	}

	waiter.Wait()
	if updateErr != nil {
		return updateErr
	}
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	return nil
}

func updateGUIAuthentication(guiCfg *config.GUIConfiguration, guiUser, guiPassword string) error {
	if guiUser != "" && guiCfg.User != guiUser {
		guiCfg.User = guiUser
		log.Println("Updated GUI authentication user name:", guiUser)
	}

	if guiPassword != "" && guiCfg.Password != guiPassword {
		if err := guiCfg.HashAndSetPassword(guiPassword); err != nil {
			return fmt.Errorf("failed to set GUI authentication password: %w", err)
		}
		log.Println("Updated GUI authentication password.")
	}
	return nil
}
