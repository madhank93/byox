#!/usr/bin/env bash
# vs.sh <course> <from> <to>  — verify cumulative 1..to, snapshot from..to if all pass
course="$1"; from="$2"; to="$3"
root="/Volumes/work/git-repos/build-your-own-x"
work="$root/reference-solutions/${course}-work"
if ! ( cd "$work" && go build -o /tmp/vs_bin ./app ); then
  echo "BUILD FAILED"; exit 1
fi
want=$(( to - from + 1 ))
out=$( "$root/reference-solutions/verify.sh" "$course" "$work" "$to" "$from" 2>&1 )
passed=$( echo "$out" | grep -ac 'Test passed' )
failed=$( echo "$out" | grep -ac 'Test failed' )
echo "verify $from..$to → passed=$passed/$want failed=$failed"
if [ "$failed" -ne 0 ] || [ "$passed" -lt "$want" ]; then
  echo "$out" | grep -aiE 'test failed|panic' | head
  exit 1
fi
"$root/reference-solutions/snapshot.sh" "$course" "$work/app/main.go" "$from" "$to"
