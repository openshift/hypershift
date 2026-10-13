#!/bin/bash
echo >&2 "Processing " "$@"

eval_cmd=("diff")
for f in "$@"; do
  eval_cmd+=("<(sed -e '/^FROM /d' -e '/COPY .* \. \./d' -e '/COPY \. \./d' \"$f\")")
done

eval "${eval_cmd[*]}"
