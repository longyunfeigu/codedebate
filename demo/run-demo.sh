#!/bin/bash
# Wrapper: prints the command prompt then runs simulation
# This makes the VHS recording look clean - only one command visible

printf "\033[0;36m❯\033[0m codedebate review 42 --reviewers claude,gpt4o,gemini\n"
sleep 0.8
bash "$(dirname "$0")/simulate.sh"
