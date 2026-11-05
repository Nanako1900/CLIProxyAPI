# Upstream updates and local development

This fork keeps upstream history and local deployment changes on separate branches:

| Branch | Purpose |
| --- | --- |
| `main` | A fast-forward-only copy of `router-for-me/CLIProxyAPI:main`. Do not commit local changes here. |
| `coolify` | Default branch for local development and Coolify deployments. |

The **Sync upstream** GitHub Actions workflow runs approximately every six hours, at minute 17 (UTC). GitHub may delay scheduled runs. It can also be started from **Actions → Sync upstream → Run workflow**. The workflow lives on the default `coolify` branch because GitHub only schedules workflows from the default branch.

Each run fetches upstream `main`, fast-forwards this fork's `main`, and opens or reuses a pull request from `main` into `coolify` if upstream has commits that have not been merged. The workflow never pushes tags, force-pushes, or merges changes into `coolify` automatically. A divergence of `main` fails the run instead of discarding local commits.

## Accept an update

1. Open the update PR in this fork.
2. Review upstream changes. Do not use GitHub's **Resolve conflicts** or **Update branch** buttons on this PR: they would modify its head branch, `main`, and break the pristine upstream mirror. Follow the integration-branch procedure below for conflicts.
3. If GitHub shows **Approve workflows to run**, approve the build check. PRs created using `GITHUB_TOKEN` may need this approval.
4. Wait for the build to pass, then use **Create a merge commit**. Do not squash or rebase upstream synchronization PRs; keeping merge ancestry prevents repeated updates.
5. Coolify can deploy the resulting push to `coolify` when its GitHub App or webhook automatic deployments have been configured.

Create feature branches from `coolify` and target your development PRs back to `coolify`. Keep the Go module path unchanged unless you intentionally change the project's package imports.

## Resolve conflicts on an integration branch

Start from an up-to-date `coolify` branch, create a branch such as `integration/upstream-<short-sha>`, and merge `origin/main` into it. Resolve conflicts and run the build on that branch. Open a PR from the integration branch into `coolify` and merge it with **Create a merge commit**. Close the original `main` → `coolify` PR once the integration PR has been merged. Never commit conflict resolutions to `main`.

## Automation credentials

Before enabling synchronization, configure a dedicated SSH deploy key with write access **only to this fork**, stored in the Actions secret `UPSTREAM_SYNC_SSH_KEY`. This avoids copying a personal GitHub token into Actions and permits syncing upstream workflow-file changes. The key can write any branch of this repository; it is not branch-scoped and does not expire automatically. The workflow itself only attempts a non-forced update of `main`. Without the secret, the workflow reports a setup error and does not modify branches.

The key is used in a temporary bare Git repository. The job does not check out or execute upstream source or actions. GitHub host keys are obtained from GitHub's HTTPS metadata API and SSH host verification stays enabled. The built-in `GITHUB_TOKEN` only receives `contents: read` and `pull-requests: write` for PR management.

To rotate the key, generate a new Ed25519 key, add the public key under **Settings → Deploy keys** with write access, replace the Actions secret with its private key, verify a manual run, and remove the old key. To disable synchronization, disable **Sync upstream** and remove both its deploy key and secret. Revoking the OAuth credential used to create a deploy key can also remove that key.

## Required repository settings

- Default branch: `coolify`.
- Merge commits enabled; squash and rebase disabled to retain upstream ancestry.
- Automatically deleting merged PR head branches disabled. Never delete `main` after merging an upstream update PR.
- Actions enabled and allowed to create pull requests. Individual jobs declare their required permissions.
- Disable the inherited `agents-md-guard`, `translator-path-guard`, `auto-retarget-main-pr-to-dev`, `docker-image`, and release workflows in this fork. They enforce upstream-specific contribution or publishing rules and are not needed for deployment. Keep the inherited `pr-test-build` workflow enabled.
- Do not add upstream Docker Hub publishing credentials. Coolify builds the source using `docker-compose.coolify.yml` and the upstream `Dockerfile`.
- New upstream workflows can become active when imported by the SSH push. Review upstream workflow changes and their permissions; disabling existing inherited workflows does not disable future new files automatically.

GitHub can disable scheduled workflows in public repositories after 60 days without repository activity. Check **Actions** if updates stop, and re-enable or manually run the workflow as needed.

See [COOLIFY_CN.md](COOLIFY_CN.md) for deployment instructions in Chinese.

References: [GitHub fork synchronization](https://docs.github.com/en/pull-requests/how-tos/work-with-forks/syncing-a-fork), [workflow triggers](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows), [GITHUB_TOKEN](https://docs.github.com/en/actions/concepts/security/github_token), [deploy keys](https://docs.github.com/en/authentication/connecting-to-github-with-ssh/managing-deploy-keys).
