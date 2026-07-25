---
name: git-branch-workflow
description: Safely choose, create, merge, release, and clean up Git branches using a lightweight GitFlow model. Use for feature development, normal bug fixes, release preparation, production deployments, emergency hotfixes, branch naming, pull-request targets, semantic version tags, and deciding between main, production, develop, feature/*, fix/*, release/*, and hotfix/*.
---

# Git Branch Workflow

## Instructions

### 1. Inspect before changing Git state

Run read-only checks first:

```bash
git status --short --branch
git branch --all --verbose
git remote -v
git log --oneline --decorate -10
```

Preserve unrelated local changes. Do not switch branches, merge, rebase, push,
delete branches, or create tags unless the user's request authorizes that
operation. Never use destructive cleanup such as `git reset --hard` to handle a
dirty worktree.

Determine the repository's actual default and production branch. Use `main` in
the guidance below; substitute `production` if that is the established
production branch. Do not maintain both `main` and `production` as equivalent
long-lived branches unless the deployment system explicitly requires both.

### 2. Use this branch model

```text
main (or production)          deployed, production-ready code
│
├── hotfix/<issue>-<slug>     emergency production repair
│
└── develop                   integration branch for upcoming work
    ├── feature/<issue>-<slug>
    ├── fix/<issue>-<slug>
    ├── chore/<issue>-<slug>
    └── release/vX.Y.Z
```

Use these responsibilities:

| Branch | Create from | Merge into | Purpose |
|---|---|---|---|
| `main` or `production` | — | — | Exact production-ready history; protect it and require reviewed pull requests |
| `develop` | `main` initially | — | Integration branch for the next release |
| `feature/*` | `develop` | `develop` | New user-visible behavior |
| `fix/*` | `develop` | `develop` | Non-emergency defect found before release |
| `chore/*`, `docs/*`, `refactor/*`, `test/*` | `develop` | `develop` | Scoped maintenance work |
| `release/vX.Y.Z` | `develop` | `main`, then back to `develop` | Stabilization, versioning, and release-only fixes |
| `hotfix/*` | `main` | `main`, then back to `develop` | Urgent production repair |

Keep `main` and `develop` long-lived. Delete short-lived branches after their
pull requests merge.

### 3. Choose the correct branch

Apply this decision order:

1. If production is broken and the repair cannot wait for the next release,
   create `hotfix/<issue>-<slug>` from `main`.
2. If preparing a tested version for production, create
   `release/vX.Y.Z` from `develop`.
3. If implementing a feature, create `feature/<issue>-<slug>` from `develop`.
4. If correcting an unreleased defect, create `fix/<issue>-<slug>` from
   `develop`.
5. If the work is documentation, tests, refactoring, build, or dependency
   maintenance, use the corresponding prefix from `develop`.
6. For a tiny project without concurrent releases or multiple contributors,
   recommend trunk-based development instead of creating `develop` and release
   branches unnecessarily.

Use lowercase kebab-case after the prefix. Include an issue ID when one exists:

```text
feature/SDK-142-workflow-cancellation
fix/SDK-167-sqlite-lease-renewal
release/v1.4.0
hotfix/SDK-181-auth-bypass
```

### 4. Implement on a short-lived branch

Update the source branch safely, then create the work branch:

```bash
git switch develop
git pull --ff-only
git switch -c feature/SDK-142-workflow-cancellation
```

Make focused commits. Prefer commit messages that state the outcome:

```text
feat(workflow): add durable cancellation
fix(storage): fence expired execution leases
docs(git): document hotfix merge-back
```

Before proposing a merge:

1. Inspect the diff and confirm no unrelated files are included.
2. Run formatting, static analysis, tests, and build commands appropriate to
   the repository.
3. Update documentation and migration notes when public behavior changes.
4. Push and open a pull request only when requested.
5. Target the branch specified in the table, not whichever branch is currently
   convenient.

### 5. Complete a release

Create a release branch only when the intended release contents are already in
`develop`:

```bash
git switch develop
git pull --ff-only
git switch -c release/v1.4.0
```

Allow only stabilization changes: tests, bug fixes, version metadata,
documentation, and release notes. Do not add unrelated features.

After release validation:

1. Merge the reviewed release branch into `main`.
2. Create an annotated semantic-version tag on the reviewed merge commit:

   ```bash
   git tag -a v1.4.0 -m "Release v1.4.0"
   ```

3. Deploy that exact tagged commit.
4. Merge `main` back into `develop` so release fixes and version changes are
   retained.
5. Delete `release/v1.4.0` after both merge paths succeed.

Do not tag or deploy if required checks fail. Do not move an existing published
tag; create a new patch version instead.

### 6. Complete a hotfix

Create emergency repairs from the deployed production branch:

```bash
git switch main
git pull --ff-only
git switch -c hotfix/SDK-181-auth-bypass
```

Keep the change minimal and add a regression test. After validation:

1. Merge the reviewed hotfix into `main`.
2. Tag and deploy a patch release such as `v1.4.1`.
3. Merge `main` back into `develop`.
4. If an active `release/*` branch exists, merge or cherry-pick the reviewed
   hotfix into it as well.
5. Delete the hotfix branch after all required merge-back paths succeed.

Never repair production only on `develop`; that allows the same defect to
reappear in the next release.

### 7. Handle divergence and conflicts safely

- Prefer `git pull --ff-only` on shared long-lived branches.
- Rebase only private, unpublished branches unless the team explicitly allows
  rewriting shared history.
- Resolve conflicts with knowledge of both changes; do not select an entire
  side blindly.
- Re-run affected tests after conflict resolution.
- Use `--force-with-lease`, never plain `--force`, only when rewriting an
  authorized private branch.
- Never delete a remote branch or tag without confirming the exact target and
  that required merge-back work is complete.

### 8. Report the result

State:

- the selected branch type and why;
- its source and pull-request target;
- checks that passed;
- whether a release tag or deployment is still required;
- whether merge-back to `develop` or an active release branch remains.

## Examples

### New payment feature

Request: `Implement payment retries for the next version.`

Use:

```text
source: develop
branch: feature/SDK-203-payment-retries
pull request target: develop
```

Do not merge directly into production.

### Release version 1.5.0

Request: `Prepare v1.5.0 for QA and production.`

Use:

```text
develop
  → release/v1.5.0
  → reviewed merge into main
  → annotated tag v1.5.0
  → merge main back into develop
```

### Urgent login failure in production

Request: `Production login is broken; ship an emergency fix.`

Use:

```text
main
  → hotfix/SDK-219-login-failure
  → reviewed merge into main
  → patch tag and deployment
  → merge main back into develop
```

### Small repository with one contributor

Request: `Should this two-file project add develop and release branches?`

Recommend protected `main` plus short-lived branches and pull requests. Add
`develop` or `release/*` only when concurrent integration and stabilization
work justify their maintenance cost.
