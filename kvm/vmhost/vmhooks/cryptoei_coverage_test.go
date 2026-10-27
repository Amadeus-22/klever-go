package vmhooks_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/elliptic"
	"encoding/hex"
	"math"
	"math/big"
	"strings"
	"testing"

	commonMock "github.com/klever-io/klever-go/common/mock"
	"github.com/klever-io/klever-go/kvm/config"
	"github.com/klever-io/klever-go/kvm/crypto/signing/secp256k1"
	"github.com/klever-io/klever-go/kvm/executor"
	contextmock "github.com/klever-io/klever-go/kvm/mock/context"
	worldmock "github.com/klever-io/klever-go/kvm/mock/world"
	"github.com/klever-io/klever-go/kvm/vmhost"
	"github.com/klever-io/klever-go/kvm/vmhost/hostCore"
	"github.com/klever-io/klever-go/kvm/vmhost/vmhooks"
	"github.com/stretchr/testify/require"
)

// newCryptoHooks builds a real VM host (real managed types + real crypto) with FixAuditChangesV6 gated by
// fixV6, and a mock WASM instance so the memory-based hooks work. Driving the crypto hooks through it in
// this package is what attributes their coverage to cryptoei.go
func newCryptoHooks(t *testing.T, fixV6 bool) *vmhooks.VMHooksImpl {
	t.Helper()
	mockWorld := worldmock.NewMockWorld()
	fc := commonMock.NewForkControllerStub()
	fc.FixAuditChangesV6Value = fixV6
	vmHost, err := hostCore.NewVMHost(mockWorld, makeHostParametersWithFork(fc))
	require.NoError(t, err)
	it, ok := vmHost.Runtime().GetInstanceTracker().(InstanceTracker)
	require.True(t, ok)
	it.ReplaceInstance(contextmock.NewInstanceMock(nil))
	hooks := vmhooks.NewVMHooksImpl(vmHost)
	provideGas(hooks, 1_000_000_000)
	return hooks
}

func decodeHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	require.NoError(t, err)
	return b
}

// shared test vectors, taken from the hosttest crypto suite
const (
	blsCheckOK   = "3e886a4c6e109a151f4105aee65a5192d150ef1fa68d3cd76964a0b086006dbe4324c989deb0e4416c6d6706db1b1910eb2732f08842fb4886067b9ed191109ac2188d76002d2e11da80a3f0ea89fee6b59c834cc478a6bd49cb8a193b1abb16@e96bd0f36b70c5ccc0c4396343bd7d8255b8a526c55fa1e218511fafe6539b8e@04725db195e37aa237cdbbda76270d4a229b6e7a3651104dc58c4349c0388e8546976fe54a04240530b99064e434c90f"
	secpKeyHex   = "04d2e670a19c6d753d1a6d8b20bd045df8a08fb162cf508956c31268c6d81ffdabab65528eefbb8057aa85d597258a3fbd481a24633bc9b47a9aa045c91371de52"
	secpRHex     = "fef45d2892953aa5bbcdb057b5e98b208f1617a7498af7eb765574e29b5d9c2c"
	secpSHex     = "2b8a9c0ad55394fb4aa21dc9483aea13279d6768ff2a9d6bcf589ac2613b3b02"
	p521PointX   = "010ba38127b62997b313aa2990a13fce55c46fc3ae751a7a7b91c41341719b57f13b9185edd96a0211acf922adb13aa9d7c64925664a9419ae6f5bc9cc4d25f91f50"
	p521PointY   = "016967055bf964609b6fd853e0aa9b90d6e1e942066278a18e8604f9fcef5b64370412f20836767829ee7e0d3fc8e2e204e2a8ec4f9257a552d66647b2d1b9856223"
	p521Marshal  = "04010ba38127b62997b313aa2990a13fce55c46fc3ae751a7a7b91c41341719b57f13b9185edd96a0211acf922adb13aa9d7c64925664a9419ae6f5bc9cc4d25f91f50016967055bf964609b6fd853e0aa9b90d6e1e942066278a18e8604f9fcef5b64370412f20836767829ee7e0d3fc8e2e204e2a8ec4f9257a552d66647b2d1b9856223"
	p521Compress = "03010ba38127b62997b313aa2990a13fce55c46fc3ae751a7a7b91c41341719b57f13b9185edd96a0211acf922adb13aa9d7c64925664a9419ae6f5bc9cc4d25f91f50"
	p521Scalar   = "f93e4ae433cc12cf2a43fc0ef26400c0e125508224cdb649380f25479148a4ad"
)

func blsVectors(t *testing.T) (key, message, sig []byte) {
	parts := strings.Split(blsCheckOK, "@")
	return decodeHex(t, parts[0]), decodeHex(t, parts[1]), decodeHex(t, parts[2])
}

func edVectors() (pub, message, sig []byte) {
	priv := ed25519.NewKeyFromSeed([]byte("1122334455667788990011223344556677"[:32]))
	message = []byte("test message!")
	return priv.Public().(ed25519.PublicKey), message, ed25519.Sign(priv, message)
}

func secpSig(t *testing.T) []byte {
	sig, err := secp256k1.NewSecp256k1().EncodeSecp256k1DERSignature(decodeHex(t, secpRHex), decodeHex(t, secpSHex))
	require.NoError(t, err)
	return sig
}

// forkModes runs a subtest with FixAuditChangesV6 both off and on
func forkModes(t *testing.T, run func(t *testing.T, hooks *vmhooks.VMHooksImpl)) {
	for _, fixV6 := range []bool{false, true} {
		name := "preFork"
		if fixV6 {
			name = "postFork"
		}
		t.Run(name, func(t *testing.T) {
			run(t, newCryptoHooks(t, fixV6))
		})
	}
}

func TestCryptoHashHooks(t *testing.T) {
	input := []byte("the quick brown fox")

	t.Run("non-managed", func(t *testing.T) {
		forkModes(t, func(t *testing.T, hooks *vmhooks.VMHooksImpl) {
			require.NoError(t, hooks.MemStore(executor.MemPtr(0), input))
			require.Equal(t, int32(0), hooks.Sha256(executor.MemPtr(0), executor.MemLength(len(input)), executor.MemPtr(500)))
			require.Equal(t, int32(0), hooks.Keccak256(executor.MemPtr(0), executor.MemLength(len(input)), executor.MemPtr(600)))
			require.Equal(t, int32(0), hooks.Ripemd160(executor.MemPtr(0), executor.MemLength(len(input)), executor.MemPtr(700)))
		})
	})

	t.Run("managed", func(t *testing.T) {
		forkModes(t, func(t *testing.T, hooks *vmhooks.VMHooksImpl) {
			mt := hooks.GetManagedTypesContext()
			in := mt.NewManagedBufferFromBytes(input)
			require.Equal(t, int32(0), hooks.ManagedSha256(in, mt.NewManagedBuffer()))
			require.Equal(t, int32(0), hooks.ManagedKeccak256(in, mt.NewManagedBuffer()))
			require.Equal(t, int32(0), hooks.ManagedRipemd160(in, mt.NewManagedBuffer()))
		})
	})
}

func TestCryptoVerifyHooks(t *testing.T) {
	blsKey, blsMsg, blsSig := blsVectors(t)
	edKey, edMsg, edSig := edVectors()
	secpKey, secpMsg, sig := decodeHex(t, secpKeyHex), decodeHex(t, "01020304"), secpSig(t)

	t.Run("non-managed", func(t *testing.T) {
		forkModes(t, func(t *testing.T, hooks *vmhooks.VMHooksImpl) {
			// BLS: key@0, message@1000, sig@2000
			require.NoError(t, hooks.MemStore(executor.MemPtr(0), blsKey))
			require.NoError(t, hooks.MemStore(executor.MemPtr(1000), blsMsg))
			require.NoError(t, hooks.MemStore(executor.MemPtr(2000), blsSig))
			require.Equal(t, int32(0), hooks.VerifyBLS(executor.MemPtr(0), executor.MemPtr(1000), executor.MemLength(len(blsMsg)), executor.MemPtr(2000)))

			require.NoError(t, hooks.MemStore(executor.MemPtr(0), edKey))
			require.NoError(t, hooks.MemStore(executor.MemPtr(1000), edMsg))
			require.NoError(t, hooks.MemStore(executor.MemPtr(2000), edSig))
			require.Equal(t, int32(0), hooks.VerifyEd25519(executor.MemPtr(0), executor.MemPtr(1000), executor.MemLength(len(edMsg)), executor.MemPtr(2000)))

			require.NoError(t, hooks.MemStore(executor.MemPtr(0), secpKey))
			require.NoError(t, hooks.MemStore(executor.MemPtr(1000), secpMsg))
			require.NoError(t, hooks.MemStore(executor.MemPtr(2000), sig))
			require.Equal(t, int32(0), hooks.VerifySecp256k1(executor.MemPtr(0), executor.MemLength(len(secpKey)), executor.MemPtr(1000), executor.MemLength(len(secpMsg)), executor.MemPtr(2000)))
			require.Equal(t, int32(0), hooks.VerifyCustomSecp256k1(executor.MemPtr(0), executor.MemLength(len(secpKey)), executor.MemPtr(1000), executor.MemLength(len(secpMsg)), executor.MemPtr(2000), int32(secp256k1.ECDSADoubleSha256)))
		})
	})

	t.Run("managed", func(t *testing.T) {
		forkModes(t, func(t *testing.T, hooks *vmhooks.VMHooksImpl) {
			mt := hooks.GetManagedTypesContext()
			require.Equal(t, int32(0), hooks.ManagedVerifyBLS(mt.NewManagedBufferFromBytes(blsKey), mt.NewManagedBufferFromBytes(blsMsg), mt.NewManagedBufferFromBytes(blsSig)))
			require.Equal(t, int32(0), hooks.ManagedVerifyEd25519(mt.NewManagedBufferFromBytes(edKey), mt.NewManagedBufferFromBytes(edMsg), mt.NewManagedBufferFromBytes(edSig)))
			require.Equal(t, int32(0), hooks.ManagedVerifySecp256k1(mt.NewManagedBufferFromBytes(secpKey), mt.NewManagedBufferFromBytes(secpMsg), mt.NewManagedBufferFromBytes(sig)))
			require.Equal(t, int32(0), hooks.ManagedVerifyCustomSecp256k1(mt.NewManagedBufferFromBytes(secpKey), mt.NewManagedBufferFromBytes(secpMsg), mt.NewManagedBufferFromBytes(sig), int32(secp256k1.ECDSADoubleSha256)))
		})
	})
}

func TestCryptoDerHook(t *testing.T) {
	r, s := decodeHex(t, secpRHex), decodeHex(t, secpSHex)
	forkModes(t, func(t *testing.T, hooks *vmhooks.VMHooksImpl) {
		mt := hooks.GetManagedTypesContext()
		require.Equal(t, int32(0), hooks.ManagedEncodeSecp256k1DerSignature(mt.NewManagedBufferFromBytes(r), mt.NewManagedBufferFromBytes(s), mt.NewManagedBuffer()))
	})
}

func TestCryptoECHooks(t *testing.T) {
	x := new(big.Int).SetBytes(decodeHex(t, p521PointX))
	y := new(big.Int).SetBytes(decodeHex(t, p521PointY))
	scalar := decodeHex(t, p521Scalar)
	marshalled := decodeHex(t, p521Marshal)
	compressed := decodeHex(t, p521Compress)
	addX, addY := elliptic.P521().Add(x, y, x, y)
	doubleX, doubleY := elliptic.P521().Double(x, y)

	t.Run("non-managed", func(t *testing.T) {
		forkModes(t, func(t *testing.T, hooks *vmhooks.VMHooksImpl) {
			mt := hooks.GetManagedTypesContext()
			ec := mt.PutEllipticCurve(elliptic.P521().Params())
			xH, yH := mt.NewBigInt(x), mt.NewBigInt(y)
			rx, ry := mt.NewBigIntFromInt64(0), mt.NewBigIntFromInt64(0)

			require.Greater(t, hooks.GetCurveLengthEC(ec), int32(0))
			require.Greater(t, hooks.GetPrivKeyByteLengthEC(ec), int32(0))
			require.Equal(t, int32(0), hooks.EllipticCurveGetValues(ec, mt.NewBigIntFromInt64(0), mt.NewBigIntFromInt64(0), mt.NewBigIntFromInt64(0), mt.NewBigIntFromInt64(0), mt.NewBigIntFromInt64(0)))
			require.Equal(t, int32(1), hooks.IsOnCurveEC(ec, xH, yH))

			hooks.AddEC(rx, ry, ec, xH, yH, xH, yH)
			requirePoint(t, mt, rx, ry, addX, addY)
			hooks.DoubleEC(rx, ry, ec, xH, yH)
			requirePoint(t, mt, rx, ry, doubleX, doubleY)

			// CreateEC parses the curve name from memory
			require.NoError(t, hooks.MemStore(executor.MemPtr(0), []byte("p521")))
			require.GreaterOrEqual(t, hooks.CreateEC(executor.MemPtr(0), executor.MemLength(4)), int32(0))

			require.NoError(t, hooks.MemStore(executor.MemPtr(100), scalar))
			require.Equal(t, int32(0), hooks.ScalarBaseMultEC(rx, ry, ec, executor.MemPtr(100), executor.MemLength(len(scalar))))
			require.Equal(t, int32(0), hooks.ScalarMultEC(rx, ry, ec, xH, yH, executor.MemPtr(100), executor.MemLength(len(scalar))))

			require.NotEqual(t, int32(-1), hooks.MarshalEC(xH, yH, ec, executor.MemPtr(200)))
			require.NotEqual(t, int32(-1), hooks.MarshalCompressedEC(xH, yH, ec, executor.MemPtr(400)))

			require.NoError(t, hooks.MemStore(executor.MemPtr(600), marshalled))
			require.Equal(t, int32(0), hooks.UnmarshalEC(rx, ry, ec, executor.MemPtr(600), executor.MemLength(len(marshalled))))
			requirePoint(t, mt, rx, ry, x, y)

			// fresh handles, so decoding the compressed form has to write them itself
			cx, cy := mt.NewBigIntFromInt64(0), mt.NewBigIntFromInt64(0)
			require.NoError(t, hooks.MemStore(executor.MemPtr(800), compressed))
			require.Equal(t, int32(0), hooks.UnmarshalCompressedEC(cx, cy, ec, executor.MemPtr(800), executor.MemLength(len(compressed))))
			requirePoint(t, mt, cx, cy, x, y)

			require.Equal(t, int32(0), hooks.GenerateKeyEC(mt.NewBigIntFromInt64(0), mt.NewBigIntFromInt64(0), ec, executor.MemPtr(1000)))
		})
	})

	t.Run("managed", func(t *testing.T) {
		forkModes(t, func(t *testing.T, hooks *vmhooks.VMHooksImpl) {
			mt := hooks.GetManagedTypesContext()
			ec := mt.PutEllipticCurve(elliptic.P521().Params())
			xH, yH := mt.NewBigInt(x), mt.NewBigInt(y)

			require.Equal(t, int32(0), hooks.ManagedScalarBaseMultEC(mt.NewBigIntFromInt64(0), mt.NewBigIntFromInt64(0), ec, mt.NewManagedBufferFromBytes(scalar)))
			require.Equal(t, int32(0), hooks.ManagedScalarMultEC(mt.NewBigIntFromInt64(0), mt.NewBigIntFromInt64(0), ec, xH, yH, mt.NewManagedBufferFromBytes(scalar)))
			require.NotEqual(t, int32(-1), hooks.ManagedMarshalEC(xH, yH, ec, mt.NewManagedBuffer()))
			require.NotEqual(t, int32(-1), hooks.ManagedMarshalCompressedEC(xH, yH, ec, mt.NewManagedBuffer()))
			rx, ry := mt.NewBigIntFromInt64(0), mt.NewBigIntFromInt64(0)
			require.Equal(t, int32(0), hooks.ManagedUnmarshalEC(rx, ry, ec, mt.NewManagedBufferFromBytes(marshalled)))
			requirePoint(t, mt, rx, ry, x, y)

			cx, cy := mt.NewBigIntFromInt64(0), mt.NewBigIntFromInt64(0)
			require.Equal(t, int32(0), hooks.ManagedUnmarshalCompressedEC(cx, cy, ec, mt.NewManagedBufferFromBytes(compressed)))
			requirePoint(t, mt, cx, cy, x, y)
			require.Equal(t, int32(0), hooks.ManagedGenerateKeyEC(mt.NewBigIntFromInt64(0), mt.NewBigIntFromInt64(0), ec, mt.NewManagedBuffer()))
			require.GreaterOrEqual(t, hooks.ManagedCreateEC(mt.NewManagedBufferFromBytes([]byte("p521"))), int32(0))
		})
	})
}

// TestCryptoScalarReductionPostFork exercises reduceScalarModOrder's reduce branch: a scalar longer than
// the curve's byte size is reduced modulo the order (not rejected), still yielding a point on the curve
func TestCryptoScalarReductionPostFork(t *testing.T) {
	hooks := newCryptoHooks(t, true)
	mt := hooks.GetManagedTypesContext()
	ec := mt.PutEllipticCurve(elliptic.P224().Params()) // 28-byte order

	longScalar := make([]byte, 40) // longer than the curve size, forces reduction
	for i := range longScalar {
		longScalar[i] = 0xAB
	}

	require.Equal(t, int32(0), hooks.ManagedScalarBaseMultEC(mt.NewBigIntFromInt64(0), mt.NewBigIntFromInt64(0), ec, mt.NewManagedBufferFromBytes(longScalar)))
}

// TestCryptoManagedLengthRejectsPostFork exercises the checkMBufferLength/checkMBufferMaxLength reject
// paths: wrong-sized keys, signatures and curve names are rejected before anything is copied
func TestCryptoManagedLengthRejectsPostFork(t *testing.T) {
	_, blsMsg, blsSig := blsVectors(t)
	edKey, edMsg, _ := edVectors()

	t.Run("BLS padded key", func(t *testing.T) {
		hooks := newCryptoHooks(t, true)
		mt := hooks.GetManagedTypesContext()
		require.Equal(t, int32(1), hooks.ManagedVerifyBLS(mt.NewManagedBufferFromBytes(make([]byte, 97)), mt.NewManagedBufferFromBytes(blsMsg), mt.NewManagedBufferFromBytes(blsSig)))
	})
	t.Run("Ed25519 padded signature", func(t *testing.T) {
		hooks := newCryptoHooks(t, true)
		mt := hooks.GetManagedTypesContext()
		require.Equal(t, int32(1), hooks.ManagedVerifyEd25519(mt.NewManagedBufferFromBytes(edKey), mt.NewManagedBufferFromBytes(edMsg), mt.NewManagedBufferFromBytes(make([]byte, 65))))
	})
	t.Run("secp256k1 key of neither size", func(t *testing.T) {
		hooks := newCryptoHooks(t, true)
		mt := hooks.GetManagedTypesContext()
		require.Equal(t, int32(1), hooks.ManagedVerifyCustomSecp256k1(mt.NewManagedBufferFromBytes(make([]byte, 34)), mt.NewManagedBufferFromBytes(edMsg), mt.NewManagedBufferFromBytes(make([]byte, 64)), int32(secp256k1.ECDSADoubleSha256)))
	})
	t.Run("UnmarshalEC wrong buffer length", func(t *testing.T) {
		hooks := newCryptoHooks(t, true)
		mt := hooks.GetManagedTypesContext()
		ec := mt.PutEllipticCurve(elliptic.P521().Params())
		require.Equal(t, int32(1), hooks.ManagedUnmarshalEC(mt.NewBigIntFromInt64(0), mt.NewBigIntFromInt64(0), ec, mt.NewManagedBufferFromBytes(make([]byte, 5))))
	})
	t.Run("UnmarshalCompressedEC wrong buffer length", func(t *testing.T) {
		hooks := newCryptoHooks(t, true)
		mt := hooks.GetManagedTypesContext()
		ec := mt.PutEllipticCurve(elliptic.P521().Params())
		require.Equal(t, int32(1), hooks.ManagedUnmarshalCompressedEC(mt.NewBigIntFromInt64(0), mt.NewBigIntFromInt64(0), ec, mt.NewManagedBufferFromBytes(make([]byte, 5))))
	})
}

// TestCryptoScalarMultOutOfGasPostFork pins, at hook level, the property this change exists for: from
// FixAuditChangesV6 a scalar multiplication the caller cannot pay for fails before the multiplication
// runs, and leaves the result handles alone. Both charges are covered, the hook's fixed cost and the
// per-byte cost of the scalar, because either one moving after the work or back to an unbounded charge
// would reopen the stall while every other test here stayed green
func TestCryptoScalarMultOutOfGasPostFork(t *testing.T) {
	// far longer than any curve order, so the multiplication would be the expensive part
	longScalar := make([]byte, 20_000)
	for i := range longScalar {
		longScalar[i] = 0xAB
	}

	// prepare builds the handles and returns the hook call, the two result handles and the curve
	type prepared struct {
		call   func() int32
		rx, ry int32
		ec     int32
	}

	scenarios := []struct {
		name    string
		prepare func(hooks *vmhooks.VMHooksImpl) prepared
	}{
		{
			name: "ManagedScalarBaseMultEC",
			prepare: func(hooks *vmhooks.VMHooksImpl) prepared {
				mt := hooks.GetManagedTypesContext()
				ec := mt.PutEllipticCurve(elliptic.P521().Params())
				rx, ry := mt.NewBigIntFromInt64(0), mt.NewBigIntFromInt64(0)
				data := mt.NewManagedBufferFromBytes(longScalar)
				return prepared{func() int32 { return hooks.ManagedScalarBaseMultEC(rx, ry, ec, data) }, rx, ry, ec}
			},
		},
		{
			name: "ManagedScalarMultEC",
			prepare: func(hooks *vmhooks.VMHooksImpl) prepared {
				mt := hooks.GetManagedTypesContext()
				ec := mt.PutEllipticCurve(elliptic.P521().Params())
				xH := mt.NewBigInt(new(big.Int).SetBytes(decodeHex(t, p521PointX)))
				yH := mt.NewBigInt(new(big.Int).SetBytes(decodeHex(t, p521PointY)))
				rx, ry := mt.NewBigIntFromInt64(0), mt.NewBigIntFromInt64(0)
				data := mt.NewManagedBufferFromBytes(longScalar)
				return prepared{func() int32 { return hooks.ManagedScalarMultEC(rx, ry, ec, xH, yH, data) }, rx, ry, ec}
			},
		},
		{
			name: "ScalarBaseMultEC",
			prepare: func(hooks *vmhooks.VMHooksImpl) prepared {
				mt := hooks.GetManagedTypesContext()
				ec := mt.PutEllipticCurve(elliptic.P521().Params())
				rx, ry := mt.NewBigIntFromInt64(0), mt.NewBigIntFromInt64(0)
				require.NoError(t, hooks.MemStore(executor.MemPtr(0), longScalar))
				return prepared{func() int32 {
					return hooks.ScalarBaseMultEC(rx, ry, ec, executor.MemPtr(0), executor.MemLength(len(longScalar)))
				}, rx, ry, ec}
			},
		},
		{
			name: "ScalarMultEC",
			prepare: func(hooks *vmhooks.VMHooksImpl) prepared {
				mt := hooks.GetManagedTypesContext()
				ec := mt.PutEllipticCurve(elliptic.P521().Params())
				xH := mt.NewBigInt(new(big.Int).SetBytes(decodeHex(t, p521PointX)))
				yH := mt.NewBigInt(new(big.Int).SetBytes(decodeHex(t, p521PointY)))
				rx, ry := mt.NewBigIntFromInt64(0), mt.NewBigIntFromInt64(0)
				require.NoError(t, hooks.MemStore(executor.MemPtr(0), longScalar))
				return prepared{func() int32 {
					return hooks.ScalarMultEC(rx, ry, ec, xH, yH, executor.MemPtr(0), executor.MemLength(len(longScalar)))
				}, rx, ry, ec}
			},
		},
	}

	// budgets mirror the hook's own arithmetic: the fixed charge is one scalar byte's worth, then one
	// more per byte of scalar
	budgets := []struct {
		name string
		gas  func(oneByte uint64) uint64
	}{
		{"one gas short of the fixed cost", func(oneByte uint64) uint64 { return oneByte - 1 }},
		{"one gas short of the per-byte cost", func(oneByte uint64) uint64 {
			return oneByte*uint64(1+len(longScalar)) - 1
		}},
	}

	for _, sc := range scenarios {
		for _, b := range budgets {
			t.Run(sc.name+"/"+b.name, func(t *testing.T) {
				hooks := newCryptoHooks(t, true)
				p := sc.prepare(hooks)

				mult := hooks.GetManagedTypesContext().GetScalarMult100xCurveGasCostMultiplier(p.ec)
				oneByte := hooks.GetMeteringContext().GasSchedule().CryptoAPICost.ScalarMultECC * uint64(mult) / 100
				require.Positive(t, oneByte, "the test gas schedule must price a scalar byte above zero")
				provideGas(hooks, b.gas(oneByte))

				require.Equal(t, int32(1), p.call())
				require.ErrorIs(t, hooks.GetRuntimeContext().GetAllErrors(), vmhost.ErrNotEnoughGas)

				mt := hooks.GetManagedTypesContext()
				x, err := mt.GetBigInt(p.rx)
				require.NoError(t, err)
				y, err := mt.GetBigInt(p.ry)
				require.NoError(t, err)
				require.Zero(t, x.Sign(), "the result handle must be untouched when the call runs out of gas")
				require.Zero(t, y.Sign(), "the result handle must be untouched when the call runs out of gas")
			})
		}
	}
}

// TestCryptoFixedCostOutOfGasPostFork covers the hooks the scalar tests above cannot reach: those whose
// only charge is the fixed hook cost, with nothing per byte behind it. If that charge is unbounded it
// overdraws, GasLeft() reports 0, and every later bounded charge of 0 passes its own 0 < 0 check, so the
// crypto work runs for an underfunded call. Each case here is one gas short of the fixed cost alone
func TestCryptoFixedCostOutOfGasPostFork(t *testing.T) {
	blsKey, _, blsSig := blsVectors(t)
	x := new(big.Int).SetBytes(decodeHex(t, p521PointX))
	y := new(big.Int).SetBytes(decodeHex(t, p521PointY))

	scenarios := []struct {
		name string
		// prepare returns the hook's fixed cost and a call that must refuse before doing any work
		prepare func(t *testing.T, hooks *vmhooks.VMHooksImpl) (uint64, func(t *testing.T))
	}{
		{
			name: "VerifyBLS",
			prepare: func(t *testing.T, hooks *vmhooks.VMHooksImpl) (uint64, func(*testing.T)) {
				require.NoError(t, hooks.MemStore(executor.MemPtr(0), blsKey))
				require.NoError(t, hooks.MemStore(executor.MemPtr(200), blsSig))
				return hooks.GetMeteringContext().GasSchedule().CryptoAPICost.VerifyBLS, func(t *testing.T) {
					// an empty message zeroes every per-byte charge, so only the fixed cost can refuse;
					// 1 is the fault code, -1 would mean the pairing ran and reported a bad signature
					require.Equal(t, int32(1), hooks.VerifyBLS(executor.MemPtr(0), executor.MemPtr(100), 0, executor.MemPtr(200)))
				}
			},
		},
		{
			name: "ManagedVerifyBLS",
			prepare: func(t *testing.T, hooks *vmhooks.VMHooksImpl) (uint64, func(*testing.T)) {
				mt := hooks.GetManagedTypesContext()
				key := mt.NewManagedBufferFromBytes(blsKey)
				msg := mt.NewManagedBufferFromBytes(nil)
				sig := mt.NewManagedBufferFromBytes(blsSig)
				return hooks.GetMeteringContext().GasSchedule().CryptoAPICost.VerifyBLS, func(t *testing.T) {
					require.Equal(t, int32(1), hooks.ManagedVerifyBLS(key, msg, sig))
				}
			},
		},
		{
			name: "EncodeSecp256k1DerSignature",
			prepare: func(t *testing.T, hooks *vmhooks.VMHooksImpl) (uint64, func(*testing.T)) {
				require.NoError(t, hooks.MemStore(executor.MemPtr(0), decodeHex(t, secpRHex)))
				require.NoError(t, hooks.MemStore(executor.MemPtr(100), decodeHex(t, secpSHex)))
				return hooks.GetMeteringContext().GasSchedule().CryptoAPICost.EncodeDERSig, func(t *testing.T) {
					require.Equal(t, int32(1), hooks.EncodeSecp256k1DerSignature(
						executor.MemPtr(0), 32, executor.MemPtr(100), 32, executor.MemPtr(200)))
				}
			},
		},
		{
			name: "ManagedCreateEC",
			prepare: func(t *testing.T, hooks *vmhooks.VMHooksImpl) (uint64, func(*testing.T)) {
				name := hooks.GetManagedTypesContext().NewManagedBufferFromBytes([]byte("p521"))
				return hooks.GetMeteringContext().GasSchedule().CryptoAPICost.EllipticCurveNew, func(t *testing.T) {
					require.Equal(t, int32(-1), hooks.ManagedCreateEC(name))
				}
			},
		},
		{
			name: "AddEC",
			prepare: func(t *testing.T, hooks *vmhooks.VMHooksImpl) (uint64, func(*testing.T)) {
				mt := hooks.GetManagedTypesContext()
				ec := mt.PutEllipticCurve(elliptic.P521().Params())
				xH, yH := mt.NewBigInt(x), mt.NewBigInt(y)
				rx, ry := mt.NewBigIntFromInt64(0), mt.NewBigIntFromInt64(0)
				mult := mt.Get100xCurveGasCostMultiplier(ec)
				cost := hooks.GetMeteringContext().GasSchedule().CryptoAPICost.AddECC * uint64(mult) / 100
				return cost, func(t *testing.T) {
					hooks.AddEC(rx, ry, ec, xH, yH, xH, yH)
					requireHandlesUntouched(t, mt, rx, ry)
				}
			},
		},
		{
			name: "DoubleEC",
			prepare: func(t *testing.T, hooks *vmhooks.VMHooksImpl) (uint64, func(*testing.T)) {
				mt := hooks.GetManagedTypesContext()
				ec := mt.PutEllipticCurve(elliptic.P521().Params())
				xH, yH := mt.NewBigInt(x), mt.NewBigInt(y)
				rx, ry := mt.NewBigIntFromInt64(0), mt.NewBigIntFromInt64(0)
				mult := mt.Get100xCurveGasCostMultiplier(ec)
				cost := hooks.GetMeteringContext().GasSchedule().CryptoAPICost.DoubleECC * uint64(mult) / 100
				return cost, func(t *testing.T) {
					hooks.DoubleEC(rx, ry, ec, xH, yH)
					requireHandlesUntouched(t, mt, rx, ry)
				}
			},
		},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			hooks := newCryptoHooks(t, true)
			cost, call := sc.prepare(t, hooks)
			require.Positive(t, cost, "the test gas schedule must price this hook above zero")
			provideGas(hooks, cost-1)

			call(t)

			require.ErrorIs(t, hooks.GetRuntimeContext().GetAllErrors(), vmhost.ErrNotEnoughGas)
		})
	}
}

// requirePoint asserts the two result handles hold the expected curve point
func requirePoint(t *testing.T, mt vmhost.ManagedTypesContext, xHandle, yHandle int32, wantX, wantY *big.Int) {
	t.Helper()
	gotX, err := mt.GetBigInt(xHandle)
	require.NoError(t, err)
	gotY, err := mt.GetBigInt(yHandle)
	require.NoError(t, err)
	require.Zero(t, gotX.Cmp(wantX), "x: got %s, want %s", gotX, wantX)
	require.Zero(t, gotY.Cmp(wantY), "y: got %s, want %s", gotY, wantY)
}

// requireHandlesUntouched asserts the void EC hooks wrote nothing into their result handles
func requireHandlesUntouched(t *testing.T, mt vmhost.ManagedTypesContext, handles ...int32) {
	t.Helper()
	for _, h := range handles {
		value, err := mt.GetBigInt(h)
		require.NoError(t, err)
		require.Zero(t, value.Sign(), "the result handle must be untouched when the call runs out of gas")
	}
}

// TestCryptoUnmarshalRejectsWrongLengthBeforeCopy pins the non-managed Unmarshal hooks' exact-length
// check, which their managed twins already had through checkMBufferLength. The budget here covers the
// fixed hook cost with a little to spare but nowhere near DataCopyPerByte for the whole range, so the
// error tells the two orders apart: rejected for free reports a wrong length, paid for first runs out
// of gas
func TestCryptoUnmarshalRejectsWrongLengthBeforeCopy(t *testing.T) {
	const wrongLength = executor.MemLength(10_000)

	tests := []struct {
		name string
		cost func(cost *config.GasCost, mult int32) uint64
		call func(hooks *vmhooks.VMHooksImpl, ec, rx, ry int32) int32
	}{
		{
			name: "UnmarshalEC",
			cost: func(c *config.GasCost, mult int32) uint64 { return c.CryptoAPICost.UnmarshalECC * uint64(mult) / 100 },
			call: func(h *vmhooks.VMHooksImpl, ec, rx, ry int32) int32 {
				return h.UnmarshalEC(rx, ry, ec, executor.MemPtr(0), wrongLength)
			},
		},
		{
			name: "UnmarshalCompressedEC",
			cost: func(c *config.GasCost, mult int32) uint64 {
				return c.CryptoAPICost.UnmarshalCompressedECC * uint64(mult) / 100
			},
			call: func(h *vmhooks.VMHooksImpl, ec, rx, ry int32) int32 {
				return h.UnmarshalCompressedEC(rx, ry, ec, executor.MemPtr(0), wrongLength)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hooks := newCryptoHooks(t, true)
			mt := hooks.GetManagedTypesContext()
			ec := mt.PutEllipticCurve(elliptic.P521().Params())
			rx, ry := mt.NewBigIntFromInt64(0), mt.NewBigIntFromInt64(0)

			schedule := hooks.GetMeteringContext().GasSchedule()
			fixed := tt.cost(schedule, mt.Get100xCurveGasCostMultiplier(ec))
			copyCost := schedule.BaseOperationCost.DataCopyPerByte * uint64(wrongLength)
			spare := fixed + copyCost/2
			require.Less(t, spare, fixed+copyCost, "the budget must not cover the copy")
			provideGas(hooks, spare)

			require.Equal(t, int32(1), tt.call(hooks, ec, rx, ry))

			err := hooks.GetRuntimeContext().GetAllErrors()
			require.ErrorIs(t, err, vmhost.ErrLengthOfBufferNotCorrect)
			// compared as text because errors.Is panics once it walks this error type past its last
			// wrapping layer, see wrappableError.Unwrap
			require.NotContains(t, err.Error(), vmhost.ErrNotEnoughGas.Error(),
				"the wrong length must be refused before it is paid for")
		})
	}
}

// TestCryptoVerifyCustomSecp256k1TruncatedHeaderPostFork pins the DER header bound. MemLoad truncates
// instead of failing when the range runs past the end of contract memory, so a signature offset at the
// very edge returns fewer than the 2 header bytes the hook asks for, and indexing the second one panics.
// From FixAuditChangesV6 that is an out-of-bounds fault instead
func TestCryptoVerifyCustomSecp256k1TruncatedHeaderPostFork(t *testing.T) {
	hooks := newCryptoHooks(t, true)
	key := decodeHex(t, secpKeyHex)
	require.NoError(t, hooks.MemStore(executor.MemPtr(0), key))

	// one byte short of the 2-byte header, so the load comes back truncated
	edge := executor.MemPtr(hooks.GetRuntimeContext().GetInstance().MemLength() - 1) // #nosec G115

	require.NotPanics(t, func() {
		require.Equal(t, int32(1), hooks.VerifyCustomSecp256k1(
			executor.MemPtr(0), executor.MemLength(len(key)),
			executor.MemPtr(1000), 4,
			edge,
			int32(secp256k1.ECDSADoubleSha256),
		))
	})
	require.ErrorIs(t, hooks.GetRuntimeContext().GetAllErrors(), vmhost.ErrBadBounds)
}

// TestCryptoPreForkGasParity pins, hook by hook, the gas the crypto hooks charge before
// FixAuditChangesV6 against the historical formula, spelled out from the gas schedule. The refactor
// split each hook's single historical charge into a fixed charge plus a per-byte charge inside the load
// helpers, and moved the managed scalar mults' fixed charge ahead of the copy: the totals are unchanged,
// and this is what says so. Without it, a change to the pre-fork branch of startTracingAndUseGas,
// loadMemBytes or loadMBufferBytes would change the gas replayed for historical blocks and nothing
// would fail.
//
// The EC rows all use p224, whose field, order, constant and coordinates are 28 bytes, under the
// 32-byte threshold above which ConsumeGasForBigIntCopy charges anything: each total is then the hook's
// own charge and nothing else. The rows that fault set their own budget, because WithFault burns
// whatever gas is left, which under a generous budget would hide the charge behind the whole budget
func TestCryptoPreForkGasParity(t *testing.T) {
	p224 := elliptic.P224()
	params := p224.Params()
	scalar := bytes.Repeat([]byte{0x2b}, 28)
	px, py := p224.ScalarBaseMult(scalar)
	marshalled := elliptic.Marshal(p224, px, py)
	compressed := elliptic.MarshalCompressed(p224, px, py)
	for _, value := range []*big.Int{params.P, params.N, params.B, params.Gx, params.Gy, px, py} {
		require.LessOrEqual(t, (value.BitLen()+7)/8, 32,
			"the curve values must stay under the threshold above which the big int copy is charged")
	}

	hashInput := []byte("the quick brown fox")
	blsKey, blsMsg, blsSig := blsVectors(t)
	edKey, edMsg, edSig := edVectors()
	secpKey, secpMsg, secpSignature := decodeHex(t, secpKeyHex), decodeHex(t, "01020304"), secpSig(t)
	r, s := decodeHex(t, secpRHex), decodeHex(t, secpSHex)
	curveName := []byte("p224")

	const (
		aOffset       = executor.MemPtr(0)
		bOffset       = executor.MemPtr(1000)
		cOffset       = executor.MemPtr(2000)
		outOffset     = executor.MemPtr(4000)
		missingHandle = int32(9999)
	)

	// putP224 puts the curve and the point above into the host, returning the managed types context,
	// the curve handle, the point handles and two fresh result handles
	putP224 := func(hooks *vmhooks.VMHooksImpl) (vmhost.ManagedTypesContext, int32, int32, int32, int32, int32) {
		mt := hooks.GetManagedTypesContext()
		return mt, mt.PutEllipticCurve(params), mt.NewBigInt(px), mt.NewBigInt(py),
			mt.NewBigIntFromInt64(0), mt.NewBigIntFromInt64(0)
	}
	// curveCost is how every EC hook scales its fixed cost by the curve
	curveCost := func(base uint64, multiplier int32) uint64 {
		return base * uint64(multiplier) / 100 // #nosec G115 - the multipliers are positive constants
	}

	tests := []struct {
		name string
		// run calls the hook once and returns the gas history charged for that call
		run     func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64
		wantErr error
	}{
		{name: "Sha256", run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
			require.NoError(t, hooks.MemStore(aOffset, hashInput))
			require.Equal(t, int32(0), hooks.Sha256(aOffset, executor.MemLength(len(hashInput)), outOffset))
			cost := hooks.GetMeteringContext().GasSchedule()
			return cost.CryptoAPICost.SHA256 + cost.BaseOperationCost.DataCopyPerByte*uint64(len(hashInput))
		}},
		{name: "Keccak256", run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
			require.NoError(t, hooks.MemStore(aOffset, hashInput))
			require.Equal(t, int32(0), hooks.Keccak256(aOffset, executor.MemLength(len(hashInput)), outOffset))
			cost := hooks.GetMeteringContext().GasSchedule()
			return cost.CryptoAPICost.Keccak256 + cost.BaseOperationCost.DataCopyPerByte*uint64(len(hashInput))
		}},
		{name: "Ripemd160", run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
			require.NoError(t, hooks.MemStore(aOffset, hashInput))
			require.Equal(t, int32(0), hooks.Ripemd160(aOffset, executor.MemLength(len(hashInput)), outOffset))
			cost := hooks.GetMeteringContext().GasSchedule()
			return cost.CryptoAPICost.Ripemd160 + cost.BaseOperationCost.DataCopyPerByte*uint64(len(hashInput))
		}},
		{name: "ManagedSha256", run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
			mt := hooks.GetManagedTypesContext()
			require.Equal(t, int32(0), hooks.ManagedSha256(mt.NewManagedBufferFromBytes(hashInput), mt.NewManagedBuffer()))
			cost := hooks.GetMeteringContext().GasSchedule()
			return cost.CryptoAPICost.SHA256 + cost.BaseOperationCost.DataCopyPerByte*uint64(len(hashInput))
		}},
		{name: "ManagedKeccak256", run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
			mt := hooks.GetManagedTypesContext()
			require.Equal(t, int32(0), hooks.ManagedKeccak256(mt.NewManagedBufferFromBytes(hashInput), mt.NewManagedBuffer()))
			cost := hooks.GetMeteringContext().GasSchedule()
			return cost.CryptoAPICost.Keccak256 + cost.BaseOperationCost.DataCopyPerByte*uint64(len(hashInput))
		}},
		{name: "ManagedRipemd160", run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
			mt := hooks.GetManagedTypesContext()
			require.Equal(t, int32(0), hooks.ManagedRipemd160(mt.NewManagedBufferFromBytes(hashInput), mt.NewManagedBuffer()))
			cost := hooks.GetMeteringContext().GasSchedule()
			return cost.CryptoAPICost.Ripemd160 + cost.BaseOperationCost.DataCopyPerByte*uint64(len(hashInput))
		}},
		{name: "VerifyBLS", run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
			require.NoError(t, hooks.MemStore(aOffset, blsKey))
			require.NoError(t, hooks.MemStore(bOffset, blsMsg))
			require.NoError(t, hooks.MemStore(cOffset, blsSig))
			require.Equal(t, int32(0), hooks.VerifyBLS(aOffset, bOffset, executor.MemLength(len(blsMsg)), cOffset))
			cost := hooks.GetMeteringContext().GasSchedule()
			return cost.CryptoAPICost.VerifyBLS + cost.BaseOperationCost.DataCopyPerByte*uint64(len(blsMsg))
		}},
		{name: "VerifyEd25519", run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
			require.NoError(t, hooks.MemStore(aOffset, edKey))
			require.NoError(t, hooks.MemStore(bOffset, edMsg))
			require.NoError(t, hooks.MemStore(cOffset, edSig))
			require.Equal(t, int32(0), hooks.VerifyEd25519(aOffset, bOffset, executor.MemLength(len(edMsg)), cOffset))
			cost := hooks.GetMeteringContext().GasSchedule()
			return cost.CryptoAPICost.VerifyEd25519 + cost.BaseOperationCost.DataCopyPerByte*uint64(len(edMsg))
		}},
		{name: "VerifyCustomSecp256k1", run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
			require.NoError(t, hooks.MemStore(aOffset, secpKey))
			require.NoError(t, hooks.MemStore(bOffset, secpMsg))
			require.NoError(t, hooks.MemStore(cOffset, secpSignature))
			require.Equal(t, int32(0), hooks.VerifyCustomSecp256k1(
				aOffset, executor.MemLength(len(secpKey)), bOffset, executor.MemLength(len(secpMsg)),
				cOffset, int32(secp256k1.ECDSADoubleSha256)))
			cost := hooks.GetMeteringContext().GasSchedule()
			return cost.CryptoAPICost.VerifySecp256k1 + cost.BaseOperationCost.DataCopyPerByte*uint64(len(secpMsg))
		}},
		{name: "VerifySecp256k1", run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
			require.NoError(t, hooks.MemStore(aOffset, secpKey))
			require.NoError(t, hooks.MemStore(bOffset, secpMsg))
			require.NoError(t, hooks.MemStore(cOffset, secpSignature))
			require.Equal(t, int32(0), hooks.VerifySecp256k1(
				aOffset, executor.MemLength(len(secpKey)), bOffset, executor.MemLength(len(secpMsg)), cOffset))
			cost := hooks.GetMeteringContext().GasSchedule()
			return cost.CryptoAPICost.VerifySecp256k1 + cost.BaseOperationCost.DataCopyPerByte*uint64(len(secpMsg))
		}},
		{name: "ManagedVerifyBLS", run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
			mt := hooks.GetManagedTypesContext()
			require.Equal(t, int32(0), hooks.ManagedVerifyBLS(
				mt.NewManagedBufferFromBytes(blsKey), mt.NewManagedBufferFromBytes(blsMsg), mt.NewManagedBufferFromBytes(blsSig)))
			cost := hooks.GetMeteringContext().GasSchedule()
			return cost.CryptoAPICost.VerifyBLS +
				cost.BaseOperationCost.DataCopyPerByte*uint64(len(blsKey)+len(blsMsg)+len(blsSig))
		}},
		{name: "ManagedVerifyEd25519", run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
			mt := hooks.GetManagedTypesContext()
			require.Equal(t, int32(0), hooks.ManagedVerifyEd25519(
				mt.NewManagedBufferFromBytes(edKey), mt.NewManagedBufferFromBytes(edMsg), mt.NewManagedBufferFromBytes(edSig)))
			cost := hooks.GetMeteringContext().GasSchedule()
			return cost.CryptoAPICost.VerifyEd25519 +
				cost.BaseOperationCost.DataCopyPerByte*uint64(len(edKey)+len(edMsg)+len(edSig))
		}},
		{name: "ManagedVerifyCustomSecp256k1", run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
			mt := hooks.GetManagedTypesContext()
			require.Equal(t, int32(0), hooks.ManagedVerifyCustomSecp256k1(
				mt.NewManagedBufferFromBytes(secpKey), mt.NewManagedBufferFromBytes(secpMsg),
				mt.NewManagedBufferFromBytes(secpSignature), int32(secp256k1.ECDSADoubleSha256)))
			cost := hooks.GetMeteringContext().GasSchedule()
			return cost.CryptoAPICost.VerifySecp256k1 +
				cost.BaseOperationCost.DataCopyPerByte*uint64(len(secpKey)+len(secpMsg)+len(secpSignature))
		}},
		{name: "ManagedVerifySecp256k1", run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
			mt := hooks.GetManagedTypesContext()
			require.Equal(t, int32(0), hooks.ManagedVerifySecp256k1(
				mt.NewManagedBufferFromBytes(secpKey), mt.NewManagedBufferFromBytes(secpMsg),
				mt.NewManagedBufferFromBytes(secpSignature)))
			cost := hooks.GetMeteringContext().GasSchedule()
			return cost.CryptoAPICost.VerifySecp256k1 +
				cost.BaseOperationCost.DataCopyPerByte*uint64(len(secpKey)+len(secpMsg)+len(secpSignature))
		}},
		{name: "EncodeSecp256k1DerSignature", run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
			require.NoError(t, hooks.MemStore(aOffset, r))
			require.NoError(t, hooks.MemStore(bOffset, s))
			require.Equal(t, int32(0), hooks.EncodeSecp256k1DerSignature(
				aOffset, executor.MemLength(len(r)), bOffset, executor.MemLength(len(s)), outOffset))
			return hooks.GetMeteringContext().GasSchedule().CryptoAPICost.EncodeDERSig
		}},
		{name: "ManagedEncodeSecp256k1DerSignature", run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
			mt := hooks.GetManagedTypesContext()
			require.Equal(t, int32(0), hooks.ManagedEncodeSecp256k1DerSignature(
				mt.NewManagedBufferFromBytes(r), mt.NewManagedBufferFromBytes(s), mt.NewManagedBuffer()))
			return hooks.GetMeteringContext().GasSchedule().CryptoAPICost.EncodeDERSig
		}},
		{name: "AddEC", run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
			mt, ec, xH, yH, rx, ry := putP224(hooks)
			hooks.AddEC(rx, ry, ec, xH, yH, xH, yH)
			cost := hooks.GetMeteringContext().GasSchedule()
			return curveCost(cost.CryptoAPICost.AddECC, mt.Get100xCurveGasCostMultiplier(ec))
		}},
		{name: "DoubleEC", run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
			mt, ec, xH, yH, rx, ry := putP224(hooks)
			hooks.DoubleEC(rx, ry, ec, xH, yH)
			cost := hooks.GetMeteringContext().GasSchedule()
			return curveCost(cost.CryptoAPICost.DoubleECC, mt.Get100xCurveGasCostMultiplier(ec))
		}},
		{name: "IsOnCurveEC", run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
			mt, ec, xH, yH, _, _ := putP224(hooks)
			require.Equal(t, int32(1), hooks.IsOnCurveEC(ec, xH, yH))
			cost := hooks.GetMeteringContext().GasSchedule()
			return curveCost(cost.CryptoAPICost.IsOnCurveECC, mt.Get100xCurveGasCostMultiplier(ec))
		}},
		{name: "ScalarBaseMultEC", run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
			mt, ec, _, _, rx, ry := putP224(hooks)
			require.NoError(t, hooks.MemStore(aOffset, scalar))
			require.Equal(t, int32(0), hooks.ScalarBaseMultEC(rx, ry, ec, aOffset, executor.MemLength(len(scalar))))
			cost := hooks.GetMeteringContext().GasSchedule()
			oneByte := curveCost(cost.CryptoAPICost.ScalarMultECC, mt.GetScalarMult100xCurveGasCostMultiplier(ec))
			return oneByte * uint64(1+len(scalar))
		}},
		{name: "ScalarMultEC", run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
			mt, ec, xH, yH, rx, ry := putP224(hooks)
			require.NoError(t, hooks.MemStore(aOffset, scalar))
			require.Equal(t, int32(0), hooks.ScalarMultEC(rx, ry, ec, xH, yH, aOffset, executor.MemLength(len(scalar))))
			cost := hooks.GetMeteringContext().GasSchedule()
			oneByte := curveCost(cost.CryptoAPICost.ScalarMultECC, mt.GetScalarMult100xCurveGasCostMultiplier(ec))
			return oneByte * uint64(1+len(scalar))
		}},
		{name: "ManagedScalarBaseMultEC", run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
			mt, ec, _, _, rx, ry := putP224(hooks)
			require.Equal(t, int32(0), hooks.ManagedScalarBaseMultEC(rx, ry, ec, mt.NewManagedBufferFromBytes(scalar)))
			cost := hooks.GetMeteringContext().GasSchedule()
			oneByte := curveCost(cost.CryptoAPICost.ScalarMultECC, mt.GetScalarMult100xCurveGasCostMultiplier(ec))
			return oneByte * uint64(1+len(scalar))
		}},
		{name: "ManagedScalarMultEC", run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
			mt, ec, xH, yH, rx, ry := putP224(hooks)
			require.Equal(t, int32(0), hooks.ManagedScalarMultEC(rx, ry, ec, xH, yH, mt.NewManagedBufferFromBytes(scalar)))
			cost := hooks.GetMeteringContext().GasSchedule()
			oneByte := curveCost(cost.CryptoAPICost.ScalarMultECC, mt.GetScalarMult100xCurveGasCostMultiplier(ec))
			return oneByte * uint64(1+len(scalar))
		}},
		{name: "MarshalEC", run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
			mt, ec, xH, yH, _, _ := putP224(hooks)
			require.Equal(t, int32(len(marshalled)), hooks.MarshalEC(xH, yH, ec, outOffset))
			cost := hooks.GetMeteringContext().GasSchedule()
			return curveCost(cost.CryptoAPICost.MarshalECC, mt.Get100xCurveGasCostMultiplier(ec))
		}},
		{name: "ManagedMarshalEC", run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
			mt, ec, xH, yH, _, _ := putP224(hooks)
			require.Equal(t, int32(len(marshalled)), hooks.ManagedMarshalEC(xH, yH, ec, mt.NewManagedBuffer()))
			cost := hooks.GetMeteringContext().GasSchedule()
			return curveCost(cost.CryptoAPICost.MarshalECC, mt.Get100xCurveGasCostMultiplier(ec))
		}},
		{name: "MarshalCompressedEC", run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
			mt, ec, xH, yH, _, _ := putP224(hooks)
			require.Equal(t, int32(len(compressed)), hooks.MarshalCompressedEC(xH, yH, ec, outOffset))
			cost := hooks.GetMeteringContext().GasSchedule()
			return curveCost(cost.CryptoAPICost.MarshalCompressedECC, mt.Get100xCurveGasCostMultiplier(ec))
		}},
		{name: "ManagedMarshalCompressedEC", run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
			mt, ec, xH, yH, _, _ := putP224(hooks)
			require.Equal(t, int32(len(compressed)), hooks.ManagedMarshalCompressedEC(xH, yH, ec, mt.NewManagedBuffer()))
			cost := hooks.GetMeteringContext().GasSchedule()
			return curveCost(cost.CryptoAPICost.MarshalCompressedECC, mt.Get100xCurveGasCostMultiplier(ec))
		}},
		{name: "UnmarshalEC", run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
			mt, ec, _, _, rx, ry := putP224(hooks)
			require.NoError(t, hooks.MemStore(aOffset, marshalled))
			require.Equal(t, int32(0), hooks.UnmarshalEC(rx, ry, ec, aOffset, executor.MemLength(len(marshalled))))
			cost := hooks.GetMeteringContext().GasSchedule()
			return curveCost(cost.CryptoAPICost.UnmarshalECC, mt.Get100xCurveGasCostMultiplier(ec))
		}},
		{name: "ManagedUnmarshalEC", run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
			mt, ec, _, _, rx, ry := putP224(hooks)
			require.Equal(t, int32(0), hooks.ManagedUnmarshalEC(rx, ry, ec, mt.NewManagedBufferFromBytes(marshalled)))
			cost := hooks.GetMeteringContext().GasSchedule()
			return curveCost(cost.CryptoAPICost.UnmarshalECC, mt.Get100xCurveGasCostMultiplier(ec))
		}},
		{name: "UnmarshalCompressedEC", run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
			mt, ec, _, _, rx, ry := putP224(hooks)
			require.NoError(t, hooks.MemStore(aOffset, compressed))
			require.Equal(t, int32(0), hooks.UnmarshalCompressedEC(rx, ry, ec, aOffset, executor.MemLength(len(compressed))))
			cost := hooks.GetMeteringContext().GasSchedule()
			return curveCost(cost.CryptoAPICost.UnmarshalCompressedECC, mt.GetUCompressed100xCurveGasCostMultiplier(ec))
		}},
		{name: "ManagedUnmarshalCompressedEC", run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
			mt, ec, _, _, rx, ry := putP224(hooks)
			require.Equal(t, int32(0), hooks.ManagedUnmarshalCompressedEC(rx, ry, ec, mt.NewManagedBufferFromBytes(compressed)))
			cost := hooks.GetMeteringContext().GasSchedule()
			return curveCost(cost.CryptoAPICost.UnmarshalCompressedECC, mt.GetUCompressed100xCurveGasCostMultiplier(ec))
		}},
		{name: "GenerateKeyEC", run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
			mt, ec, _, _, xPub, yPub := putP224(hooks)
			require.Equal(t, int32(0), hooks.GenerateKeyEC(xPub, yPub, ec, outOffset))
			cost := hooks.GetMeteringContext().GasSchedule()
			return curveCost(cost.CryptoAPICost.GenerateKeyECC, generateKeyMultiplier(mt.Get100xCurveGasCostMultiplier(ec)))
		}},
		{name: "ManagedGenerateKeyEC", run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
			mt, ec, _, _, xPub, yPub := putP224(hooks)
			require.Equal(t, int32(0), hooks.ManagedGenerateKeyEC(xPub, yPub, ec, mt.NewManagedBuffer()))
			cost := hooks.GetMeteringContext().GasSchedule()
			return curveCost(cost.CryptoAPICost.GenerateKeyECC, generateKeyMultiplier(mt.Get100xCurveGasCostMultiplier(ec)))
		}},
		{name: "CreateEC", run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
			require.NoError(t, hooks.MemStore(aOffset, curveName))
			require.GreaterOrEqual(t, hooks.CreateEC(aOffset, executor.MemLength(len(curveName))), int32(0))
			return hooks.GetMeteringContext().GasSchedule().CryptoAPICost.EllipticCurveNew
		}},
		{name: "ManagedCreateEC", run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
			mt := hooks.GetManagedTypesContext()
			require.GreaterOrEqual(t, hooks.ManagedCreateEC(mt.NewManagedBufferFromBytes(curveName)), int32(0))
			return hooks.GetMeteringContext().GasSchedule().CryptoAPICost.EllipticCurveNew
		}},
		{name: "GetCurveLengthEC", run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
			_, ec, _, _, _, _ := putP224(hooks)
			require.Positive(t, hooks.GetCurveLengthEC(ec))
			return hooks.GetMeteringContext().GasSchedule().BigIntAPICost.BigIntGetInt64
		}},
		{name: "GetPrivKeyByteLengthEC", run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
			_, ec, _, _, _, _ := putP224(hooks)
			require.Positive(t, hooks.GetPrivKeyByteLengthEC(ec))
			return hooks.GetMeteringContext().GasSchedule().BigIntAPICost.BigIntGetInt64
		}},
		{name: "EllipticCurveGetValues", run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
			mt, ec, _, _, _, _ := putP224(hooks)
			require.Equal(t, int32(0), hooks.EllipticCurveGetValues(ec,
				mt.NewBigIntFromInt64(0), mt.NewBigIntFromInt64(0), mt.NewBigIntFromInt64(0),
				mt.NewBigIntFromInt64(0), mt.NewBigIntFromInt64(0)))
			return hooks.GetMeteringContext().GasSchedule().BigIntAPICost.BigIntGetInt64 * 5
		}},
		{
			// a negative length makes the per-byte charge saturate, as it always did, and SetPointsUsed
			// clamps what lands on the instance
			name: "Sha256 with a negative length",
			run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
				require.Equal(t, int32(1), hooks.Sha256(aOffset, -1, outOffset))
				return math.MaxInt64
			},
			wantErr: executor.ErrMemoryNegativeLength,
		},
		{
			// the negative length is refused before any charge, so a zero budget is enough to run the
			// hook: anything charged first would show up as gas used
			name: "ScalarBaseMultEC with a negative length",
			run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
				_, ec, _, _, rx, ry := putP224(hooks)
				provideGas(hooks, 0)
				require.Equal(t, int32(1), hooks.ScalarBaseMultEC(rx, ry, ec, aOffset, -1))
				return 0
			},
			wantErr: vmhost.ErrNegativeLength,
		},
		{
			// the budget is exactly the fixed cost, so a charge sneaking in before the failed copy
			// pushes gas used past it; WithFault then burns whatever is left, which is why it is not
			// simply generous here
			name: "ManagedSha256 with a missing buffer",
			run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
				fixed := hooks.GetMeteringContext().GasSchedule().CryptoAPICost.SHA256
				provideGas(hooks, fixed)
				require.Equal(t, int32(1), hooks.ManagedSha256(missingHandle, hooks.GetManagedTypesContext().NewManagedBuffer()))
				return fixed
			},
			wantErr: vmhost.ErrNoManagedBufferUnderThisHandle,
		},
		{
			name: "ManagedScalarBaseMultEC with a missing buffer",
			run: func(t *testing.T, hooks *vmhooks.VMHooksImpl) uint64 {
				mt, ec, _, _, rx, ry := putP224(hooks)
				cost := hooks.GetMeteringContext().GasSchedule()
				fixed := curveCost(cost.CryptoAPICost.ScalarMultECC, mt.GetScalarMult100xCurveGasCostMultiplier(ec))
				provideGas(hooks, fixed)
				require.Equal(t, int32(1), hooks.ManagedScalarBaseMultEC(rx, ry, ec, missingHandle))
				return fixed
			},
			wantErr: vmhost.ErrNoManagedBufferUnderThisHandle,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hooks := newCryptoHooks(t, false)

			want := tt.run(t, hooks)

			require.Equal(t, want, hooks.GetRuntimeContext().GetPointsUsed(),
				"pre-fork gas must stay on the historical formula")
			if tt.wantErr != nil {
				require.ErrorIs(t, hooks.GetRuntimeContext().GetAllErrors(), tt.wantErr)
				return
			}
			require.NoError(t, hooks.GetRuntimeContext().GetAllErrors(), "the hook must have done its work")
		})
	}
}

// generateKeyMultiplier mirrors the bump commonGenerateEC applies to the curve multiplier
func generateKeyMultiplier(multiplier int32) int32 {
	if multiplier == 250 {
		return 500
	}
	return multiplier
}
