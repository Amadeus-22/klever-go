;; Regression contract for the singlepass native stack leaks fixed in
;; klever-io/wasmer (KLC-2785, KLC-2786). Rebuild output/stack_leak.wasm with
;; `wat2wasm stack_leak.wat -o output/stack_leak.wasm`.
;;
;; Every endpoint takes one argument, the number of loop iterations, and
;; returns 7. On a VM built with the unfixed compiler the loops leak native
;; stack and the call fails with an out-of-stack error instead:
;;   elseLeak         - all architectures (then-arm result spilled at `else`)
;;   callSpilledArgs  - arm64 only (two spilled call arguments released at once)
;;   brIfSpilled      - arm64 only (spilled values released on a taken `br_if`)
(module
  (import "env" "smallIntGetUnsignedArgument" (func $getArg (param i32) (result i64)))
  (import "env" "smallIntFinishUnsigned" (func $finish (param i64)))
  (memory 1)
  (export "memory" (memory 0))

  (func $touch)
  (func $sink (param i64 i64))

  (func (export "init"))

  (func (export "elseLeak")
    (local $i i32) (local $n i32) (local $x i64)
    i32.const 0
    call $getArg
    i32.wrap_i64
    local.set $n
    i64.const 7
    local.set $x
    (loop $l
      local.get $x
      local.get $x
      local.get $x
      local.get $x
      local.get $x
      local.get $x
      local.get $x
      local.get $x
      local.get $x
      local.get $x
      local.get $x
      local.get $x
      local.get $i
      i32.const 1
      i32.or
      (if (result i64)
        (then local.get $x)
        (else local.get $x))
      drop
      drop
      drop
      drop
      drop
      drop
      drop
      drop
      drop
      drop
      drop
      drop
      drop
      call $touch
      local.get $i
      i32.const 1
      i32.add
      local.tee $i
      local.get $n
      i32.lt_u
      br_if $l)
    local.get $x
    call $finish)

  (func (export "callSpilledArgs")
    (local $i i32) (local $n i32) (local $x i64)
    i32.const 0
    call $getArg
    i32.wrap_i64
    local.set $n
    i64.const 7
    local.set $x
    (loop $l
      local.get $x
      local.get $x
      local.get $x
      local.get $x
      local.get $x
      local.get $x
      local.get $x
      local.get $x
      local.get $x
      local.get $x
      local.get $x
      local.get $x
      call $sink
      drop
      drop
      drop
      drop
      drop
      drop
      drop
      drop
      drop
      drop
      local.get $i
      i32.const 1
      i32.add
      local.tee $i
      local.get $n
      i32.lt_u
      br_if $l)
    local.get $x
    call $finish)

  (func (export "brIfSpilled")
    (local $i i32) (local $n i32) (local $x i64)
    i32.const 0
    call $getArg
    i32.wrap_i64
    local.set $n
    i64.const 7
    local.set $x
    (loop $l
      call $touch
      local.get $x
      local.get $x
      local.get $x
      local.get $x
      local.get $x
      local.get $x
      local.get $x
      local.get $x
      local.get $x
      local.get $x
      local.get $x
      local.get $x
      local.get $i
      i32.const 1
      i32.add
      local.tee $i
      local.get $n
      i32.lt_u
      br_if $l
      drop
      drop
      drop
      drop
      drop
      drop
      drop
      drop
      drop
      drop
      drop
      drop
    )
    local.get $x
    call $finish)
)
