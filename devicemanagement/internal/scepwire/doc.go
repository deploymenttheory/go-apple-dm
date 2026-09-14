// Package scepwire builds and checks the CMS messages used for SCEP enrollment.
//
// Requests and replies select their cryptographic algorithms for each message.
// This avoids changing the upstream encoder's process-wide defaults while other
// callers may be using them. Encrypted messages use AES-128-CBC with RSA key
// transport.
//
// CheckEnvelope accepts bounded BER input and rejects unsupported encryption.
// Callers must verify the original CMS signature before checking the envelope
// and must decrypt the original bytes afterward.
//
// The message formats and algorithms follow RFC 8894 and RFC 5652.
package scepwire
