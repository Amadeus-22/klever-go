(module
 (type $newFromParts (func (param i32 i32 i32) (result i32)))
 (type $pow (func (param i32 i32 i32)))
 (import "env" "bigFloatNewFromParts" (func $bigFloatNewFromParts (type $newFromParts)))
 (import "env" "bigFloatPow" (func $bigFloatPow (type $pow)))
 (memory (export "memory") 1)
 (func (export "powOneMaxExponent")
  (local $one i32)
  (local $result i32)
  i32.const 1
  i32.const 0
  i32.const 0
  call $bigFloatNewFromParts
  local.set $one
  i32.const 0
  i32.const 0
  i32.const 0
  call $bigFloatNewFromParts
  local.set $result
  local.get $result
  local.get $one
  i32.const 2147483647
  call $bigFloatPow))
