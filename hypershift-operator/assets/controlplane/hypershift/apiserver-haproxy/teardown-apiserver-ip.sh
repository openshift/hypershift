#!/usr/bin/env bash
set -x
ip addr delete {{ .ExternalAPIAddress }}/32 dev lo
ip route del {{ .ExternalAPIAddress }}/32 dev lo scope link src {{ .ExternalAPIAddress }}
