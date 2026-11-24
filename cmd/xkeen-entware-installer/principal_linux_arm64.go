package main

import (
	"errors"
	"os"
)

func lockAdmission(role string, uid, gid uint32) (*os.File, error) {
	lock, err := lockRole("principal-admission")
	if err != nil {
		return nil, err
	}
	if role == "agent-transport" {
		if _, err := os.Lstat(applicationProfile); !errors.Is(err, os.ErrNotExist) {
			lock.Close()
			return nil, errors.New("managed application profile already claims the agent install")
		}
	}
	otherUID, otherGID, err := otherPrincipal(role)
	if err != nil || uid == otherUID || gid == otherGID {
		lock.Close()
		return nil, errors.New("agent and server require separate service identities")
	}
	if role == "server" {
		application, err := loadApplicationProfile()
		if (err != nil && !errors.Is(err, os.ErrNotExist)) ||
			(err == nil && application.overlapsPrincipal(uid, gid)) {
			lock.Close()
			return nil, errors.New("agent and server require separate service identities")
		}
	}

	return lock, nil
}

func otherPrincipal(role string) (uint32, uint32, error) {
	path, directory := serverProfilePath, serverConfig
	if role == "server" {
		path, directory = profilePath, profileDir
	}
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return 0, 0, nil
	} else if err != nil {
		return 0, 0, err
	}
	if err := rootDirs(false, directory); err != nil {
		return 0, 0, err
	}
	if role == "server" {
		agent, err := readProfile()
		return agent.UID, agent.GID, err
	}
	server, err := loadServerProfile()
	return server.UID, server.GID, err
}
