//  Copyright 2019 Google Inc. All Rights Reserved.
//
//  Licensed under the Apache License, Version 2.0 (the "License");
//  you may not use this file except in compliance with the License.
//  You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
//  Unless required by applicable law or agreed to in writing, software
//  distributed under the License is distributed on an "AS IS" BASIS,
//  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//  See the License for the specific language governing permissions and
//  limitations under the License.

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/GoogleCloudPlatform/guest-logging-go/logger"
)

func runService(ctx context.Context) {
	run(ctx)
}

func obtainLock() {
	lockFile := "/run/lock/osconfig_agent.lock"

	err := os.Mkdir(filepath.Dir(lockFile), 1777)
	if err != nil && !os.IsExist(err) {
		logger.Fatalf("Cannot obtain agent lock: %v", err)
	}

	f, err := os.OpenFile(lockFile, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil && !os.IsExist(err) {
		logger.Fatalf("Cannot obtain agent lock: %v", err)
	}

	c := make(chan error)
	go func() {
		c <- syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
	}()
	select {
	case err := <-c:
		if err != nil {
			logger.Fatalf("Cannot obtain agent lock, is the agent already running? Error: %v", err)
		}
	case <-time.After(time.Second):
		logger.Fatalf("OSConfig agent lock already held, is the agent already running?")
	}

	deferredFuncs = append(deferredFuncs, func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close(); os.Remove(lockFile) })
}

func wuaUpdates(ctx context.Context, _ string) error {
	return errors.New("wuaUpdates not implemented on linux")
}
