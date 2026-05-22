#!/usr/bin/env sh

LANG=C grep -rnP '[\x80-\xFF]' cmd/ internal/ examples/ Makefile *.md
if [ $? = 0 ]; then
  exit 1
else
  exit 0
fi
