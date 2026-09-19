# Portable identity fixture

`fixture.macho` is the universal arm64/x86_64 executable built from `fixture.c`.
It returns success and contains no third-party application code. The committed
bytes let Linux and Windows compare portable results with Apple's native output.

Built on macOS 27 on 19 September 2026:

```sh
/Library/Developer/CommandLineTools/usr/bin/clang -arch arm64 -arch x86_64 \
  -isysroot /Library/Developer/CommandLineTools/SDKs/MacOSX.sdk \
  -mmacosx-version-min=11.0 fixture.c -o fixture.macho
codesign --force --sign - --identifier com.deploymenttheory.identity.fixture fixture.macho
codesign -d --verbose=4 --arch arm64 fixture.macho
codesign -d --verbose=4 --arch x86_64 fixture.macho
```

Both slices report the identifier `com.deploymenttheory.identity.fixture`, an
ad-hoc signature, no team identifier, and a SHA-256 code directory. Apple's hashes:

| Architecture | CDHash |
|---|---|
| arm64 | `7fe0792159dba657a8dce283963273c74e35cba8` |
| x86_64 | `c167705737e365fa79b9bff6927b7594bed5a190` |

Rebuilding with a different toolchain can change the hashes. Review regenerated
bytes with Apple's tools and update the recorded expectations together.
