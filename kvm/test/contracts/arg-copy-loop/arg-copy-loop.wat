(module
 (type $bigIntGetUnsignedArgument (func (param i32 i32)))
 (type $getArgument (func (param i32 i32) (result i32)))
 (import "env" "bigIntGetUnsignedArgument" (func $bigIntGetUnsignedArgument (type $bigIntGetUnsignedArgument)))
 (import "env" "getArgument" (func $getArgument (type $getArgument)))
 (memory (export "memory") 20)
 (func (export "bigIntArgLoop")
  (loop $again
   i32.const 0
   i32.const 0
   call $bigIntGetUnsignedArgument
   br $again))
 (func (export "getArgumentLoop")
  (loop $again
   i32.const 0
   i32.const 0
   call $getArgument
   drop
   br $again)))
