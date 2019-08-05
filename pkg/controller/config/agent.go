/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package config

import (
	"path/filepath"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/sirupsen/logrus"
)

// Agent watches a path and automatically loads the config stored
// therein.
type Agent struct {
	mut     sync.RWMutex // do not export Lock, etc methods
	c       *Configuration
	watcher *fsnotify.Watcher
}

// Start will begin polling the config file at the path. If the first load
// fails, Start will return the error and abort. Future load failures will log
// the failure message but continue attempting to load.
func (ca *Agent) Start(configLocation string) error {
	c, err := Load(configLocation)
	if err != nil {
		return err
	}
	ca.Set(c)
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		logrus.WithError(err).Fatal("Error when creating watcher.")
	}
	ca.watcher = watcher
	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				logrus.WithField("event.Name", event.Name).Info("modified file in the watched folder.")
				if c, err := Load(configLocation); err != nil {
					logrus.WithField("configLocation", configLocation).
						WithError(err).Error("Error loading config.")
				} else {
					ca.Set(c)
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				logrus.WithError(err).Error("Error occurred (watcher.Errors).")
			}
		}
	}()
	watchingDir := filepath.Dir(configLocation)
	logrus.WithField("watchingDir", watchingDir).Info("watch dir")
	return watcher.Add(watchingDir)
}

// Stop will stop polling the config file.
func (ca *Agent) Stop() {
	if err := ca.watcher.Close(); err != nil {
		logrus.WithError(err).Error("Error occurred  when  closing watcher.")
	}
}

// Getter returns the current Config in a thread-safe manner.
type Getter func() *Configuration

// Config returns the latest config. Do not modify the config.
func (ca *Agent) Config() *Configuration {
	ca.mut.RLock()
	defer ca.mut.RUnlock()
	return ca.c
}

// Set sets the config. Useful for testing.
func (ca *Agent) Set(c *Configuration) {
	ca.mut.Lock()
	defer ca.mut.Unlock()
	ca.c = c
}
