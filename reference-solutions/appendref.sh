#!/usr/bin/env bash
# appendref.sh <from> <to>  — append that line range of the reference into redis-work main.go
root="/Volumes/work/git-repos/build-your-own-x"
ref="$root/reference-solutions/redis-final-reference.go.txt"
work="$root/reference-solutions/redis-work/app/main.go"
printf '\n' >> "$work"
sed -n "${1},${2}p" "$ref" >> "$work"
