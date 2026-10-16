# Vendored dependencies (release bytes)

Each directory is a release's own files, extracted unchanged from the tarball named here (fetched 2026-10-14 on the
devbox; the npm integrity strings are the registry's `dist.integrity`, re-computed on the downloaded files and
equal). Nothing in these directories is edited; `foundry.toml`'s remappings let Hyperlane's sources compile as they
are.

| Directory | Release | Tarball | Integrity / sha256 | What was kept |
|---|---|---|---|---|
| `hyperlane-core/` | `@hyperlane-xyz/core@12.2.0` (2026-10-02, monorepo commit `ae1dfdd13be888f5015040214a907b19e371464f`) | `https://registry.npmjs.org/@hyperlane-xyz/core/-/core-12.2.0.tgz` | `sha512-PoXNUI+RtRjjS2dcLnoTXHmG9mlyJYK31J8Cb82VtU9d1ujQz5Krv+qkavFMWx4arWPsLL0vSn1OfijQ7n4Ajw==`; sha1 `38560955fc22e022b1d9ecd6a8663189fcbd2b45`; sha256 `239a17ce7550ace6ba5af2f9674f02d72b63efed44112f5b78327bee8c648bad` | `package/contracts/` (the Solidity sources), `package/dist/buildArtifact.json` (the release's standard-JSON compiler input: solc 0.8.33+commit.64118f21, cancun, optimizer 10,000 runs, 343 sources — what an audit compiles to compare bytecode), `package/package.json`, `package/README.md`; the tarball's other 19,800 files (typechain, zksync and tron builds, its own vendored dependencies) are not kept |
| `openzeppelin-contracts/` | `@openzeppelin/contracts@4.9.3` | `https://registry.npmjs.org/@openzeppelin/contracts/-/contracts-4.9.3.tgz` | `sha512-He3LieZ1pP2TNt5JbkPA4PNT9WC3gOTOlDcFGJW4Le4QKqwmiNJCRt44APfxMxvq7OugU/cqYuPcSBzOw38DAg==`; sha1 `00d7a8cf35a475b160b3f0293a6403c511099364`; sha256 `00d83cdf0802dd4d1eb8e146e7046a72eb7bbb8f8aeda1243d9d7d48584cb1c0` | every file but `package/build/` (the compiled JSON artifacts) |
| `openzeppelin-contracts-upgradeable/` | `@openzeppelin/contracts-upgradeable@4.9.3` | `https://registry.npmjs.org/@openzeppelin/contracts-upgradeable/-/contracts-upgradeable-4.9.3.tgz` | `sha512-jjaHAVRMrE4UuZNfDwjlLGDxTHWIOwTJS2ldnc278a0gevfXfPr8hxKEVBGFBE96kl2G3VHDZhUimw/+G3TG2A==`; sha1 `ff17a80fb945f5102571f8efecb5ce5915cc4811`; sha256 `62ea1421cd0eecb4efe62a906d5754fae5854f1ce3bb8d38236d65b8f6a679a0` | every file but `package/build/` |
| `forge-std/` | forge-std `v1.16.2` (2026-06-30, commit `bf647bd6046f2f7da30d0c2bf435e5c76a780c1b`) | `https://codeload.github.com/foundry-rs/forge-std/tar.gz/refs/tags/v1.16.2` | sha256 `35dbdf9c98db566f150a70b723f11d831feebb01ce615245a4fcaa4652fd4275` | the whole tarball |

Hyperlane 12.2.0 pins OpenZeppelin 4.9.3 itself (its `foundry.toml` and `soldeer.lock`), which is why these two
OpenZeppelin releases are the ones vendored: the Mailbox, HypERC20, MerkleTreeHook and ProtocolFee compiled here are
the release's sources against the release's dependencies.

To check a directory against its tarball:

```sh
curl -sSfLO https://registry.npmjs.org/@hyperlane-xyz/core/-/core-12.2.0.tgz
openssl dgst -sha512 -binary core-12.2.0.tgz | base64   # the integrity above, after "sha512-"
mkdir x && tar xzf core-12.2.0.tgz -C x package/contracts package/dist/buildArtifact.json package/package.json package/README.md
diff -r x/package eth/bridge/contracts/lib/hyperlane-core && echo identical
```
