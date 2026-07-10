#!/bin/sh
set -eu

: "${FASTCLAW_BOOTSTRAP_ADMIN_PASSWORD:?FASTCLAW_BOOTSTRAP_ADMIN_PASSWORD is required}"

fastclaw agents init "AiphaBee Template" \
	--id agt_1180f3adbf5bbf6608 \
	--ensure \
	--username aiphabee-admin \
	--email fastclaw-control@aiphabee.internal \
	--password "${FASTCLAW_BOOTSTRAP_ADMIN_PASSWORD}" \
	--display-name "AiphaBee Control" \
	--description "Template Agent for dedicated AiphaBee research Agents" \
	--no-start

fastclaw apikey ensure \
	--name aiphabee-control \
	--token-env FASTCLAW_CONTROL_API_KEY

fastclaw workspace-smoke

unset FASTCLAW_BOOTSTRAP_ADMIN_PASSWORD
unset FASTCLAW_CONTROL_API_KEY
exec fastclaw gateway
