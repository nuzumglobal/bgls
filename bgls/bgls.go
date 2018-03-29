// Copyright (C) 2018 Authors
// distributed under Apache 2.0 license

package bgls

import (
	"crypto/rand"
	"math/big"

	. "github.com/Project-Arda/bgls/curves"
)

//MultiSig holds set of keys and one message plus signature
type MultiSig struct {
	keys []Point
	sig  Point
	msg  []byte
}

//AggSig holds paired sequences of keys and messages, and one signature
type AggSig struct {
	keys []Point
	msgs [][]byte
	sig  Point
}

//KeyGen generates a *big.Int and Point2
func KeyGen(curve CurveSystem) (*big.Int, Point, error) {
	x, err := rand.Int(rand.Reader, curve.GetG1Order())
	if err != nil {
		return nil, nil, err
	}
	pubKey := LoadPublicKey(curve, x)
	return x, pubKey, nil
}

//LoadPublicKey turns secret key into a public key of type Point2
func LoadPublicKey(curve CurveSystem, sk *big.Int) Point {
	pubKey := curve.GetG2().Mul(sk)
	return pubKey
}

//Sign creates a signature on a message with a private key
func Sign(curve CurveSystem, sk *big.Int, m []byte) Point {
	return SignCustHash(sk, m, curve.HashToG1)
}

// SignCustHash creates a signature on a message with a private key, using
// a supplied function to hash to g1.
func SignCustHash(sk *big.Int, m []byte, hash func([]byte) Point) Point {
	h := hash(m)
	i := h.Mul(sk)
	return i
}

// VerifySingleSignature checks that a signature is valid
func VerifySingleSignature(curve CurveSystem, pubKey Point, m []byte, sig Point) bool {
	return VerifySingleSignatureCustHash(curve, pubKey, m, sig, curve.HashToG1)
}

// VerifySingleSignatureCustHash checks that a signature is valid with the supplied hash function
func VerifySingleSignatureCustHash(curve CurveSystem, pubKey Point, msg []byte, sig Point, hash func([]byte) Point) bool {
	c := make(chan PointT)
	go concurrentPair(curve, sig, curve.GetG2(), c)
	go concurrentMsgPair(curve, msg, pubKey, c)
	e1 := <-c
	e2 := <-c
	return e1.Equals(e2)
}

// AggregatePoints takes the sum of points on G2. This is used to sum a set of public keys for the multisignature
func AggregatePoints(points []Point) Point {
	if len(points) == 2 { // No parallelization needed
		aggG2, _ := points[0].Add(points[1])
		return aggG2
	}
	// Aggregate all the g2 points together using concurrency
	c := make(chan Point)
	aggPoint := make([]Point, (len(points)/2)+(len(points)%2))
	counter := 0

	// Initialize aggKeys to an array with elements being the sum of two
	// adjacent Points.
	for i := 0; i < len(points); i += 2 {
		go concurrentAggregatePoints(i, points, c)
		counter++
	}
	for i := 0; i < counter; i++ {
		aggPoint[i] = <-c
	}

	// Keep on aggregating every pair of points until only one aggregate points remains
	for {
		nxtAggPoint := make([]Point, (len(aggPoint)/2)+(len(aggPoint)%2))
		counter = 0
		if len(aggPoint) == 1 {
			break
		}
		for i := 0; i < len(aggPoint); i += 2 {
			go concurrentAggregatePoints(i, aggPoint, c)
			counter++
		}
		for i := 0; i < counter; i++ {
			nxtAggPoint[i] = <-c
		}
		aggPoint = nxtAggPoint
	}
	return aggPoint[0]
}

// concurrentAggregatePoints handles the channel for concurrent Aggregation of g2 points.
// It only adds the element at points[start] and points[start + 1], and sends it through the channel
func concurrentAggregatePoints(start int, points []Point, c chan Point) {
	if start+1 >= len(points) {
		c <- points[start]
		return
	}
	summed, _ := points[start].Add(points[start+1])
	c <- summed
}

func (a *AggSig) Verify(curve CurveSystem) bool {
	return VerifyAggregateSignature(curve, a.sig, a.keys, a.msgs)
}

// VerifyAggregateSignature verifies that the aggregated signature proves that all messages were signed by associated keys
// Will fail if there are duplicate messages, due to the possibility of the rogue public-key attack.
// If duplicate messages should be allowed, one of the protections against the rogue public-key attack should be used
// such as Knowledge of Secret Key (Kosk), enforcing distinct messages, or the method discussed
// here <https://crypto.stanford.edu/~dabo/pubs/papers/BLSmultisig.html>
func VerifyAggregateSignature(curve CurveSystem, aggsig Point, keys []Point, msgs [][]byte) bool {
	return verifyAggSig(curve, aggsig, keys, msgs, false)
}

func verifyAggSig(curve CurveSystem, aggsig Point, keys []Point, msgs [][]byte, allowDuplicates bool) bool {
	if len(keys) != len(msgs) {
		return false
	}
	if !allowDuplicates {
		if containsDuplicateMessage(msgs) {
			return false
		}
	}
	c := make(chan PointT)
	c2 := make(chan PointT)
	go concurrentPair(curve, aggsig, curve.GetG2(), c2)
	for i := 0; i < len(msgs); i++ {
		go concurrentMsgPair(curve, msgs[i], keys[i], c)
	}
	e1 := <-c2
	e2 := <-c
	for i := 1; i < len(msgs); i++ {
		e3 := <-c
		e2, _ = e2.Add(e3)
	}
	return e1.Equals(e2)
}

// concurrentPair pairs pt with key, and sends the result down the channel.
func concurrentPair(curve CurveSystem, pt Point, key Point, c chan PointT) {
	targetPoint, _ := curve.Pair(pt, key)
	c <- targetPoint
}

// concurrentMsgPair hashes the message, pairs it with key, and sends the result down the channel.
func concurrentMsgPair(curve CurveSystem, msg []byte, key Point, c chan PointT) {
	h := curve.HashToG1(msg)
	concurrentPair(curve, h, key, c)
}

func containsDuplicateMessage(msgs [][]byte) bool {
	hashmap := make(map[string]bool)
	for i := 0; i < len(msgs); i++ {
		msg := string(msgs[i])
		if _, ok := hashmap[msg]; !ok {
			hashmap[msg] = true
		} else {
			return true
		}
	}
	return false
}

type indexedPoint struct {
	index int
	pt    Point
}

func scalePoints(pts []Point, factors []*big.Int) (newKeys []Point) {
	if factors == nil {
		return pts
	} else if len(pts) != len(factors) {
		return nil
	}
	newKeys = make([]Point, len(pts))
	c := make(chan *indexedPoint)
	for i := 0; i < len(pts); i++ {
		go concurrentScale(pts[i], factors[i], i, c)
	}
	for i := 0; i < len(pts); i++ {
		pt := <-c
		newKeys[pt.index] = pt.pt
	}
	return newKeys
}

func concurrentScale(key Point, factor *big.Int, index int, c chan *indexedPoint) {
	if factor == nil {
		c <- &indexedPoint{index, key.Copy()}
	} else {
		c <- &indexedPoint{index, key.Mul(factor)}
	}
}
