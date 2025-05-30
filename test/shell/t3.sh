#!/usr/bin/env -S bash -e

. "$(dirname "$0")/../util.sh"

if in_env travis jenkins; then
	# inotify doesn't seem to work properly on travis
	echo "Travis and Jenkins give wonky results here, skipping test!"
	exit
fi

if in_env github; then
	# TODO: consider debugging this
	echo "This is failing in github, skipping test!"
	exit
fi

# setup
mkdir -p "${MGMT_TMPDIR}"mgmt{A..C}

# run till completion
$TIMEOUT "$MGMT" run --converged-timeout=5 --no-watch --tmp-prefix yaml t3-a.yaml &
pid1=$!
$TIMEOUT "$MGMT" run --converged-timeout=5 --no-watch --tmp-prefix yaml t3-b.yaml &
pid2=$!
$TIMEOUT "$MGMT" run --converged-timeout=5 --no-watch --tmp-prefix yaml t3-c.yaml &
pid3=$!

wait $pid1	# get exit status
e1=$?
wait $pid2	# get exit status
e2=$?
wait $pid3	# get exit status
e3=$?

# A: collected
test -e "${MGMT_TMPDIR}"mgmtA/f3b
test -e "${MGMT_TMPDIR}"mgmtA/f3c
test -e "${MGMT_TMPDIR}"mgmtA/f4b
test -e "${MGMT_TMPDIR}"mgmtA/f4c

# A: local
test -e "${MGMT_TMPDIR}"mgmtA/f1a
test -e "${MGMT_TMPDIR}"mgmtA/f2a
test -e "${MGMT_TMPDIR}"mgmtA/f3a
test -e "${MGMT_TMPDIR}"mgmtA/f4a

# A: nope!
test ! -e "${MGMT_TMPDIR}"mgmtA/f1b
test ! -e "${MGMT_TMPDIR}"mgmtA/f2b
test ! -e "${MGMT_TMPDIR}"mgmtA/f1c
test ! -e "${MGMT_TMPDIR}"mgmtA/f2c

# B: collected
test -e "${MGMT_TMPDIR}"mgmtB/f3a
test -e "${MGMT_TMPDIR}"mgmtB/f3c
test -e "${MGMT_TMPDIR}"mgmtB/f4a
test -e "${MGMT_TMPDIR}"mgmtB/f4c

# B: local
test -e "${MGMT_TMPDIR}"mgmtB/f1b
test -e "${MGMT_TMPDIR}"mgmtB/f2b
test -e "${MGMT_TMPDIR}"mgmtB/f3b
test -e "${MGMT_TMPDIR}"mgmtB/f4b

# B: nope!
test ! -e "${MGMT_TMPDIR}"mgmtB/f1a
test ! -e "${MGMT_TMPDIR}"mgmtB/f2a
test ! -e "${MGMT_TMPDIR}"mgmtB/f1c
test ! -e "${MGMT_TMPDIR}"mgmtB/f2c

# C: collected
test -e "${MGMT_TMPDIR}"mgmtC/f3a
test -e "${MGMT_TMPDIR}"mgmtC/f3b
test -e "${MGMT_TMPDIR}"mgmtC/f4a
test -e "${MGMT_TMPDIR}"mgmtC/f4b

# C: local
test -e "${MGMT_TMPDIR}"mgmtC/f1c
test -e "${MGMT_TMPDIR}"mgmtC/f2c
test -e "${MGMT_TMPDIR}"mgmtC/f3c
test -e "${MGMT_TMPDIR}"mgmtC/f4c

# C: nope!
test ! -e "${MGMT_TMPDIR}"mgmtC/f1a
test ! -e "${MGMT_TMPDIR}"mgmtC/f2a
test ! -e "${MGMT_TMPDIR}"mgmtC/f1b
test ! -e "${MGMT_TMPDIR}"mgmtC/f2b

exit $(($e1+$e2+$e3))
