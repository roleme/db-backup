#!/usr/bin/env bash
# shellcheck shell=bash

test_no_private_strings() {
  local hits
  hits=$("$HERE/hygiene.sh")
  assert_eq "$hits" "" "the repository must not mention private infrastructure"
  pass "no private strings"
}
