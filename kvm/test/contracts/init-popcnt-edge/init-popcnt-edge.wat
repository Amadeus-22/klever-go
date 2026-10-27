;; popcnt edge values in both widths, then a popcnt loop over 0..999.
;; Expected finishes: 1, 1, 0, 64, 32, 9864. Every executor build must agree.
(module
  (type (;0;) (func (param i64)))
  (type (;1;) (func))
  (import "env" "int64finish" (func $fin (type 0)))
  (func $init (type 1)
    (local $i i64) (local $acc i64)
    i64.const 1  i64.popcnt call $fin
    i32.const 1  i32.popcnt i64.extend_i32_u call $fin
    i64.const 0  i64.popcnt call $fin
    i64.const -1 i64.popcnt call $fin
    i32.const -1 i32.popcnt i64.extend_i32_u call $fin
    block $done
      loop $l
        local.get $i i64.const 1000 i64.ge_u br_if $done
        local.get $acc local.get $i i64.popcnt i64.add local.set $acc
        local.get $acc local.get $i i32.wrap_i64 i32.popcnt i64.extend_i32_u i64.add local.set $acc
        local.get $i i64.const 1 i64.add local.set $i
        br $l
      end
    end
    local.get $acc call $fin)
  (table (;0;) 1 1 funcref)
  (memory (;0;) 2)
  (global (;0;) (mut i32) (i32.const 66560))
  (export "memory" (memory 0))
  (export "init" (func $init)))
