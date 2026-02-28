#!/bin/bash
# CodeDebate demo simulation - mimics real codedebate review output
# Used for VHS recording

# Colors
CYAN='\033[0;36m'
CYAN_BOLD='\033[1;36m'
WHITE='\033[0;37m'
MAGENTA_BOLD='\033[1;35m'
GREEN_BOLD='\033[1;32m'
GREEN='\033[0;32m'
RED_BOLD='\033[1;31m'
RED='\033[0;31m'
YELLOW='\033[0;33m'
BLUE='\033[0;34m'
DIM='\033[0;90m'
NC='\033[0m'

sleep 0.5

# Header
echo ""
printf "${CYAN}  ==================================================${NC}\n"
printf "${CYAN_BOLD}  CodeDebate Code Review${NC}\n"
printf "${CYAN}  ==================================================${NC}\n"
echo ""
printf "${WHITE}  Target:      PR #42 - Add user authentication${NC}\n"
printf "${WHITE}  Reviewers:   claude, gpt4o, gemini${NC}\n"
printf "${WHITE}  Max Rounds:  3${NC}\n"
printf "${WHITE}  Convergence: enabled${NC}\n"
printf "${WHITE}  Context:     enabled${NC}\n"
echo ""

sleep 1

# Phase 1 - Context
printf "\n${DIM}  Phase 1/3  ${WHITE}System Context${NC}\n"
printf "${DIM}  ──────────────────────────────────────────────────${NC}\n"
sleep 0.5
printf "${DIM}Affected Modules:${NC}\n"
printf "  ${RED}●${NC} ${DIM}auth (3 files)${NC}\n"
printf "  ${YELLOW}●${NC} ${DIM}middleware (2 files)${NC}\n"
printf "  ${GREEN}●${NC} ${DIM}config (1 file)${NC}\n"
echo ""

sleep 0.8

# Phase 1 - Analyzer
printf "\n${DIM}  Phase 1/3  ${WHITE}Analyzer${NC}\n"
printf "${DIM}  ──────────────────────────────────────────────────${NC}\n"
sleep 0.3
printf "${MAGENTA_BOLD}──────────────────────────────────────────────────${NC}\n"
printf "${MAGENTA_BOLD}  Analysis${NC}\n"
printf "${MAGENTA_BOLD}──────────────────────────────────────────────────${NC}\n"
echo ""
printf "  Key changes: JWT auth middleware, token validation,\n"
printf "  session management. Focus areas: token expiry handling,\n"
printf "  SQL injection in user lookup, CORS configuration.\n"
echo ""

sleep 1

# Phase 2 - Round 1
printf "\n${DIM}  Phase 2/3  ${WHITE}Debate Round 1/3${NC}\n"
printf "${DIM}  ──────────────────────────────────────────────────${NC}\n"
sleep 0.5

# Claude
printf "\n${CYAN_BOLD}┌─ claude ${DIM}[Round 1/3]${NC}\n"
printf "${CYAN}│${NC}\n"
sleep 0.3
printf "│ Found potential race condition in session refresh.\n"
printf "│ Token validation lacks constant-time comparison\n"
printf "│ → timing attack vulnerability.\n"
printf "│ SQL query in getUserByEmail uses string concat\n"
printf "│ instead of parameterized queries.\n"
echo ""

sleep 0.6

# GPT-4o
printf "\n${CYAN_BOLD}┌─ gpt4o ${DIM}[Round 1/3]${NC}\n"
printf "${CYAN}│${NC}\n"
sleep 0.3
printf "│ JWT secret is hardcoded in config.go:42.\n"
printf "│ Missing rate limiting on /auth/login endpoint.\n"
printf "│ Token expiry set to 30 days — too long for\n"
printf "│ security-sensitive operations.\n"
echo ""

sleep 0.6

# Gemini
printf "\n${CYAN_BOLD}┌─ gemini ${DIM}[Round 1/3]${NC}\n"
printf "${CYAN}│${NC}\n"
sleep 0.3
printf "│ CORS allows wildcard origin (*) — should\n"
printf "│ restrict to specific domains. Agree with claude\n"
printf "│ on SQL injection risk. Also: error messages leak\n"
printf "│ internal DB schema details.\n"
echo ""

sleep 1

# Phase 2 - Round 2
printf "\n${DIM}  Phase 2/3  ${WHITE}Debate Round 2/3${NC}\n"
printf "${DIM}  ──────────────────────────────────────────────────${NC}\n"
sleep 0.5

# Claude Round 2
printf "\n${CYAN_BOLD}┌─ claude ${DIM}[Round 2/3]${NC}\n"
printf "${CYAN}│${NC}\n"
sleep 0.3
printf "│ Agree with gpt4o on hardcoded secret — critical.\n"
printf "│ Adding: the session store doesn't implement\n"
printf "│ proper cleanup, leaked goroutines on shutdown.\n"
printf "│ Challenge gemini: CORS wildcard is only on\n"
printf "│ dev config, prod config looks correct.\n"
echo ""

sleep 0.6

# GPT-4o Round 2
printf "\n${CYAN_BOLD}┌─ gpt4o ${DIM}[Round 2/3]${NC}\n"
printf "${CYAN}│${NC}\n"
sleep 0.3
printf "│ Confirmed: SQL injection is real — checked the\n"
printf "│ query builder path. claude's timing attack point\n"
printf "│ is valid. Additional: password hashing uses MD5\n"
printf "│ instead of bcrypt — high severity.\n"
echo ""

sleep 0.6

# Gemini Round 2
printf "\n${CYAN_BOLD}┌─ gemini ${DIM}[Round 2/3]${NC}\n"
printf "${CYAN}│${NC}\n"
sleep 0.3
printf "│ Accept claude's correction on CORS — withdrawing\n"
printf "│ that point. Agree on all SQL injection and crypto\n"
printf "│ issues. Adding: missing input validation on\n"
printf "│ password length (accepts empty string).\n"
echo ""

sleep 0.8

# Convergence Check
printf "\n${DIM}  Phase 2/3  ${WHITE}Convergence Check${NC}\n"
printf "${DIM}  ──────────────────────────────────────────────────${NC}\n"
sleep 0.3
printf "${DIM}│ Reviewers reached consensus on 7 of 8 issues.${NC}\n"
printf "${DIM}│ Verdict: CONVERGED — ending debate early.${NC}\n"
echo ""

sleep 0.8

# Phase 3 - Final Conclusion
printf "${GREEN_BOLD}\n══════════════════════════════════════════════════${NC}\n"
printf "${GREEN_BOLD}  Final Conclusion${NC}\n"
printf "${GREEN_BOLD}══════════════════════════════════════════════════${NC}\n"
echo ""
printf "  This PR introduces authentication but has several\n"
printf "  security issues that must be addressed before merge.\n"
printf "  Most critical: SQL injection, hardcoded secrets,\n"
printf "  and weak password hashing.\n"
echo ""

sleep 0.5

# Issues Table
printf "${MAGENTA_BOLD}──────────────────────────────────────────────────${NC}\n"
printf "${MAGENTA_BOLD}  Issues Found (7 unique, 12 total across reviewers)${NC}\n"
printf "${MAGENTA_BOLD}──────────────────────────────────────────────────${NC}\n"
echo ""

printf "${RED_BOLD}   1. [CRITICAL] SQL injection in user lookup${NC}\n"
printf "      ${DIM}internal/auth/user.go:67${NC}  [${CYAN}claude${NC}, ${CYAN}gpt4o${NC}, ${CYAN}gemini${NC}]\n"
printf "${GREEN}      Fix: Use parameterized queries with db.Query()${NC}\n"
echo ""

printf "${RED_BOLD}   2. [CRITICAL] Hardcoded JWT secret${NC}\n"
printf "      ${DIM}config/config.go:42${NC}  [${CYAN}gpt4o${NC}, ${CYAN}claude${NC}]\n"
printf "${GREEN}      Fix: Move to environment variable JWT_SECRET${NC}\n"
echo ""

printf "${RED}   3. [HIGH    ] MD5 password hashing${NC}\n"
printf "      ${DIM}internal/auth/password.go:15${NC}  [${CYAN}gpt4o${NC}]\n"
printf "${GREEN}      Fix: Replace with bcrypt (cost factor 12+)${NC}\n"
echo ""

printf "${RED}   4. [HIGH    ] Timing attack on token comparison${NC}\n"
printf "      ${DIM}internal/auth/token.go:89${NC}  [${CYAN}claude${NC}]\n"
printf "${GREEN}      Fix: Use crypto/subtle.ConstantTimeCompare()${NC}\n"
echo ""

printf "${YELLOW}   5. [MEDIUM  ] Missing rate limiting on login${NC}\n"
printf "      ${DIM}internal/middleware/auth.go:23${NC}  [${CYAN}gpt4o${NC}, ${CYAN}gemini${NC}]\n"
echo ""

printf "${YELLOW}   6. [MEDIUM  ] Token expiry too long (30 days)${NC}\n"
printf "      ${DIM}internal/auth/token.go:12${NC}  [${CYAN}gpt4o${NC}]\n"
echo ""

printf "${BLUE}   7. [LOW     ] Error messages leak DB schema${NC}\n"
printf "      ${DIM}internal/auth/handler.go:34${NC}  [${CYAN}gemini${NC}]\n"
echo ""

sleep 0.5

# Token Usage
printf "${DIM}──────────────────────────────────────────────────${NC}\n"
printf "${DIM}  Token Usage (Estimated)${NC}\n"
printf "${DIM}──────────────────────────────────────────────────${NC}\n"
printf "${DIM}  claude       In: 12,450   Out: 3,280   \$0.18${NC}\n"
printf "${DIM}  gpt4o        In: 11,890   Out: 2,950   \$0.09${NC}\n"
printf "${DIM}  gemini       In: 12,100   Out: 3,120   \$0.05${NC}\n"
printf "${DIM}  summarizer   In:  8,640   Out: 1,870   \$0.06${NC}\n"
printf "${DIM}  ──────────────────────────────────────────────${NC}\n"
printf "${DIM}  Total        In: 45,080   Out: 11,220  \$0.38${NC}\n"
printf "${DIM}  Converged at round 2 of 3 (saved ~33%% tokens)${NC}\n"
echo ""
printf "${GREEN}  ✓ Posted 7 comments to PR #42${NC}\n"
echo ""
