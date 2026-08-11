# Release Notes

Tagged MoltSSH releases use hybrid release notes. GitHub generates the pull
request categories, contributor list, and full changelog link from
`.github/release.yml`. An optional checked-in file at
`docs/releases/<tag>.md` is prepended as the curated release preamble.

If the tag-specific file is absent, the workflow publishes the generated notes
without a curated preamble. Workflow reruns regenerate the automated section
before updating an existing tagged release.

## Curated Preamble

Keep the checked-in preamble short and limited to information that cannot be
reliably inferred from pull requests:

- User-facing highlights.
- Compatibility or migration notes.
- Security boundaries and known limitations.
- Verification details that materially affect release confidence.

Do not repeat `What's Changed`, contributor lists, individual pull requests, or
the full changelog link. GitHub appends those sections automatically.

Use one source line per Markdown paragraph or list item instead of hard-wrapping
prose to a fixed column. GitHub controls the rendered page width.

## Pull Request Labels

Apply one of these labels before merging a pull request:

- `enhancement`
- `bug`
- `documentation`
- `breaking-change`
- `skip-changelog`

Pull requests without a matching category label appear under `Other Changes`.

## Preview

After merging the release pull request and before creating the tag, preview
GitHub's generated section without creating or editing a release:

```bash
gh api \
  --method POST \
  repos/JingYiJun/MoltSSH/releases/generate-notes \
  -f tag_name=vX.Y.Z \
  -f target_commitish=<release-commit> \
  --jq .body
```

Once the tag exists, GitHub ignores `target_commitish`; the pre-tag preview is
the last opportunity to correct labels before the workflow publishes the
release.
