package vmhooks

// KDATokenDecodedLen exposes kdaTokenDecodedLen to the external test package, so the tests price
// the KDA reads with the production formula rather than a copy of it
var KDATokenDecodedLen = kdaTokenDecodedLen
