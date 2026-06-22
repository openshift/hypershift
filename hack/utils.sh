#!/bin/sh

get_container_engine() {
	if command -v podman >/dev/null 2>&1 && command podman info >/dev/null 2>&1; then # 'command podman machine list --format {{.LastUp}} | grep -q "Currently running"' can also be used instead of 'podman info' when running on Mac or Windows
		echo podman
		retval=0
	elif command -v docker >/dev/null 2>&1 && command docker info >/dev/null 2>&1; then
		echo docker
		retval=0
	else
		echo >&2 "No container runtime found"
		retval=1
	fi
	return $retval
}

"$@"
