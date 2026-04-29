## Summary
<!-- What does this PR change? -->

## Why
<!-- Link to issue, or explain the motivation -->

Closes #

## Testing
- [ ] `go build -o playmore` succeeds
- [ ] Manual smoke test: register, upload, review, deploy via API
- [ ] Affected user flows still work in the browser

## Security checklist
<!-- Tick whichever apply -->
- [ ] No new endpoints accept user-controlled HTML without escaping
- [ ] No new SQL is built via string concat (parameterized only)
- [ ] No new endpoints expose `err.Error()` to clients
- [ ] If admin-sensitive: blocked from API key auth via `middleware.IsAPIKeyAuth`
- [ ] If state-changing: rate-limited
- [ ] If file upload: size capped, content-type validated, path traversal protected
