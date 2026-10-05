# Vendored `ranger` prelude crate

Copied from [terotests/Ranger](https://github.com/terotests/Ranger)
`runtime/rust/ranger` at commit `4283a27d5ca90646bd1fe267fd8f74d2802ec26a`
(its `[workspace]` table removed so it builds as a path dependency here).

Strict Rust modules (`use ranger::prelude::*;`) build against it with cargo;
`rgrc` lowers the same files to Ranger's other targets.
