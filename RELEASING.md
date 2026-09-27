# Releasing

How a version of Guardana Control is cut, and how anyone checks what was
published. A release is built, signed and published by
`.github/workflows/release.yml` and nowhere else; nothing is uploaded from a
laptop.

## Who may release

A maintainer with the maintain or admin role on `guardana/control`. The
repository refuses a `v*` tag pushed by anyone else, and the release job runs
in the `release` environment, which admits only `v*` tags.

## Cutting a release

1. Give the version its section in `CHANGELOG.md`, with the date:
   `## [0.1.0-alpha] - 2026-09-25`. The workflow publishes that section as the
   release notes and fails if it is missing, undated or empty.
   `scripts/release-notes.sh v0.1.0-alpha` prints what it will publish.
2. Commit that on `main`, and let CI finish green on the commit.
3. Tag the commit and push the tag:

   ```sh
   git tag -a v0.1.0-alpha -m "Guardana Control 0.1.0-alpha"
   git push origin v0.1.0-alpha
   ```

A version with a pre-release part, such as `-alpha` or `-rc.1`, is published as
a pre-release.

## What the workflow does

1. Builds `guardana-control` and `guardana-gateway` for Linux and macOS on
   amd64 and arm64, from the tagged commit, with the version set from the tag.
2. Packs one `.tar.gz` per platform with both binaries, `LICENSE`, `NOTICE`,
   `README.md` and `CHANGELOG.md`, writes a CycloneDX bill of materials for each
   archive and a `checksums.txt` with the SHA-256 of every file.
3. Signs `checksums.txt` with cosign, keyless: the certificate names this
   workflow and the tag. The signature is `checksums.txt.sigstore.json`.
4. Pushes the image `ghcr.io/guardana/control-gateway` under the staging tag
   `sha-<commit>`, for linux/amd64 and linux/arm64: `guardana-gateway` alone,
   as a distroless base's nonroot user.
5. Drafts the GitHub release with those files.
6. Signs the image, and records build provenance for it and for every file of
   the release.
7. Verifies all of it as a user would, tags the image with the version, then
   publishes the draft with the image's digest in its notes.

The workflow refuses a tag whose commit is not on `main`, and publishes the
draft only when it holds exactly the files it verified. If it fails before it
drafts the release, there is no release; if it fails later, the draft stays
unpublished. Delete the draft, if any, and the image's staging version, fix the
cause and release the next version.

GitHub makes a new package private. After the first push, a maintainer makes
`control-gateway` public (Package settings, Change visibility) and links it to
this repository if needed.

## A dry run

Run the workflow by hand (Actions, Release, Run workflow). It builds the same
archives under a `0.0.0-snapshot.<commit>` version, unsigned, and keeps them as a run
artifact for seven days. It also runs the image for the runner's platform. It
publishes nothing. `make release-snapshot` builds the same locally, and needs a
running Docker daemon.

## A tag never moves

A published tag is never deleted, moved or reused, and a published release is
never edited to hold other files. If a release is broken, say so in the next
version's changelog and release that version.

## Checking a download

Take the archive you want, `checksums.txt` and `checksums.txt.sigstore.json`
from the release page, then, for version `v0.1.0-alpha`:

```sh
sha256sum --ignore-missing -c checksums.txt

cosign verify-blob checksums.txt \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity "https://github.com/guardana/control/.github/workflows/release.yml@refs/tags/v0.1.0-alpha" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com

gh attestation verify guardana-control_0.1.0-alpha_linux_amd64.tar.gz \
  --repo guardana/control \
  --cert-identity "https://github.com/guardana/control/.github/workflows/release.yml@refs/tags/v0.1.0-alpha" \
  --source-ref refs/tags/v0.1.0-alpha --deny-self-hosted-runners
```

On macOS use `shasum -a 256 --ignore-missing -c checksums.txt`. The first
command says the archive is the one the checksum file names, the second that
this repository's release workflow signed the checksum file for that tag, and
the third that the archive was built by that workflow from that tag.

## Checking the image

Anyone with write access to the repository or package can overwrite a
registry tag or a release's notes. Take the digest from the notes and check it
with cosign v3 or later. The signature covers the index, not each platform's
image. For `v0.2.0`:

```sh
image=ghcr.io/guardana/control-gateway@sha256:<the digest in the release notes>

cosign verify "$image" \
  --certificate-identity "https://github.com/guardana/control/.github/workflows/release.yml@refs/tags/v0.2.0" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com

gh attestation verify "oci://$image" \
  --repo guardana/control \
  --cert-identity "https://github.com/guardana/control/.github/workflows/release.yml@refs/tags/v0.2.0" \
  --source-ref refs/tags/v0.2.0 --deny-self-hosted-runners
```
