#!/bin/sh
# crawl4ai-render-service/build-the-render-service-into-its-deploy-directory.sh: the build of the
# render service as a deployed site of its own, from grounding's repository. A deploy runs it by /bin/sh in the checkout, as the site's own account, with HOME the deploy's
# home (STATE/deploy/home) and ALLOD_DEPLOY_PROGRAM_OUTPUT naming the program to write.
#
# It keeps a Python venv holding crawl4ai, and Playwright's Chromium, in HOME, each named by the
# sha256 of the pinned requirements, so a push that leaves them alone reuses both and a change
# builds afresh beside the old (the running service keeps the old until it restarts). It copies
# this commit's render service beside the program, and writes the program: a launcher giving the
# service a writable home in the site's state (crawl4ai keeps a small database there) and running
# it on the socket the unit names.
#
# Debian packages it needs on the host, as the site's deploy-build-packages: python3-venv, and
# Chromium's libraries and fonts as Playwright lists them for Debian 13 (README "The browser renderer: crawl4ai").
set -eu

service_directory="$(cd "$(dirname "$0")" && pwd)"
requirements_file="$service_directory/requirements-pinned.txt"
[ -n "${ALLOD_DEPLOY_PROGRAM_OUTPUT:-}" ] || { echo "ALLOD_DEPLOY_PROGRAM_OUTPUT is not set: run by the deploy" >&2; exit 2; }
[ -n "${HOME:-}" ] && [ -d "$HOME" ] || { echo "HOME is not a directory" >&2; exit 2; }

requirements_hash="$(sha256sum "$requirements_file" 2>/dev/null || shasum -a 256 "$requirements_file")"
requirements_hash="$(printf '%s' "$requirements_hash" | cut -c1-16)"
venv_directory="$HOME/crawl4ai-venv-$requirements_hash"
browsers_directory="$HOME/playwright-browsers-$requirements_hash"

if [ ! -f "$venv_directory/.complete" ]; then
  echo "building the venv $venv_directory"
  rm -rf "$venv_directory" "$browsers_directory"
  python3 -m venv "$venv_directory"
  "$venv_directory/bin/pip" install --quiet --no-cache-dir --upgrade pip
  "$venv_directory/bin/pip" install --quiet --no-cache-dir -r "$requirements_file"
  PLAYWRIGHT_BROWSERS_PATH="$browsers_directory" "$venv_directory/bin/python" -m playwright install chromium
  touch "$venv_directory/.complete"
else
  echo "reusing the venv $venv_directory: the pinned requirements have not changed"
fi

# Venvs and browsers from older requirements, kept one deploy for a service still running them.
for old in "$HOME"/crawl4ai-venv-* "$HOME"/playwright-browsers-*; do
  case "$old" in
    "$venv_directory"|"$browsers_directory") ;;
    *) if [ -e "$old" ] && [ -n "$(find "$old" -maxdepth 0 -mmin +1440 2>/dev/null)" ]; then rm -rf "$old"; fi ;;
  esac
done

program_directory="$(dirname "$ALLOD_DEPLOY_PROGRAM_OUTPUT")"
commit="$(git rev-parse --short=12 HEAD 2>/dev/null || date +%s)"
service_copy="$program_directory/render_pages_with_crawl4ai_on_a_unix_socket.$commit.py"
cp "$service_directory/render_pages_with_crawl4ai_on_a_unix_socket.py" "$service_copy"
# Keep the two newest copies: this one, and the one a running service may still have open.
ls -t "$program_directory"/render_pages_with_crawl4ai_on_a_unix_socket.*.py 2>/dev/null \
  | sed -n '3,$p' | while read -r stale; do rm -f -- "$stale"; done

cat >"$ALLOD_DEPLOY_PROGRAM_OUTPUT" <<LAUNCHER
#!/bin/sh
# Written by grounding's crawl4ai-render-service build for commit $commit; the next deploy replaces it.
# Usage: PROGRAM SOCKET_PATH (the unit's command passes the socket's path).
set -eu
state_directory="\$(cd "\$(dirname "\$0")/../.." && pwd)"
export HOME="\$state_directory/crawl4ai-home"
mkdir -p "\$HOME"
export PLAYWRIGHT_BROWSERS_PATH="$browsers_directory"
# crawl4ai otherwise advertises its paid cloud in the journal on every blocked page.
export CRAWL4AI_NO_CLOUD_NOTICE=1
exec "$venv_directory/bin/python" "$service_copy" "\$@"
LAUNCHER
chmod 0750 "$ALLOD_DEPLOY_PROGRAM_OUTPUT"
echo "wrote the launcher for commit $commit"
