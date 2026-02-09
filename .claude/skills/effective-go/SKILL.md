---
name: Effective Go
description: "Apply Go best practices, idioms, and conventions from golang.org/doc/effective_go. Use when writing, reviewing, or refactoring Go code to ensure idiomatic, clean, and efficient implementations."
---

# Effective Go

Apply best practices and conventions from the official [Effective Go guide](https://go.dev/doc/effective_go) to write clean, idiomatic Go code.

## When to Apply

Use this skill automatically when:
- Writing new Go code
- Reviewing Go code
- Refactoring existing Go implementations

## Key Reminders

Follow the conventions and patterns documented at https://go.dev/doc/effective_go, with particular attention to:

- **Formatting**: Always use `gofmt` - this is non-negotiable
- **Documentation**: Document all exported symbols, starting with the symbol name

### Error Handling

- Wrap errors with context using `fmt.Errorf("context: %w", err)`.
- Never ignore errors silently (no `_ = someFunc()` without explicit justification).
- Use sentinel errors or custom error types where appropriate.
- Check errors immediately after the call that produces them.
- Return errors rather than panicking.

### Naming Conventions

- Use MixedCaps for exported names, mixedCaps for unexported. No underscores.
- Acronyms should be all caps (e.g., `HTTPClient`, `APIURL`).
- Interface names should not have an `I` prefix; single-method interfaces should use the `-er` suffix.
- Package names should be lowercase, single-word, and not plural.
- Receiver names should be short (1-2 letters), consistent, and not `this` or `self`.

### Code Structure

- Functions should do one thing and do it well.
- Keep functions short and focused (guideline: under 50 lines for most functions).
- Use early returns to reduce nesting.
- Group related declarations together.
- Use named return values only when they improve documentation.

### Concurrency Patterns

- Use channels for communication, mutexes for state protection.
- Don't leak goroutines; ensure proper lifecycle management.
- Use `context.Context` for cancellation propagation.

### Go Idioms

- Prefer `var` declarations for zero values, `:=` for initialized values.
- Use `make` for slices/maps when initial capacity is known.
- Prefer composition over inheritance (embedding).
- Use `defer` for cleanup, but be aware of loop/performance implications.
- Accept interfaces, return concrete types.
- Keep interfaces small (1-3 methods ideal).

## References

- Official Guide: https://go.dev/doc/effective_go
- Code Review Comments: https://github.com/golang/go/wiki/CodeReviewComments
- Standard Library: Use as reference for idiomatic patterns
