#!/bin/sh

get_container_engine() {
	if command -v podman >/dev/null 2>&1; then
		echo podman
		retval=0
	elif command -v docker >/dev/null 2>&1; then
		echo docker
		retval=0
	else
		echo >&2 "No container runtime found"
		retval=1
	fi
	return $retval
}

"$@"
