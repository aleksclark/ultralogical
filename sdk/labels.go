package sdk

import corev1 "github.com/aleksclark/ultracore/gen/go/core/v1"

// LabelEq builds an equality label selector.
func LabelEq(key, value string) *corev1.LabelSelector {
	return &corev1.LabelSelector{Key: key, Op: "=", Values: []string{value}}
}

// LabelIn builds a set-membership label selector.
func LabelIn(key string, values ...string) *corev1.LabelSelector {
	return &corev1.LabelSelector{Key: key, Op: "in", Values: values}
}

// Labels is a fluent builder for a list of selectors (AND).
type Labels struct {
	sels []*corev1.LabelSelector
}

// Eq appends an equality selector.
func (l Labels) Eq(key, value string) Labels {
	l.sels = append(l.sels, LabelEq(key, value))
	return l
}

// In appends a set-membership selector.
func (l Labels) In(key string, values ...string) Labels {
	l.sels = append(l.sels, LabelIn(key, values...))
	return l
}

// Build returns the selector slice.
func (l Labels) Build() []*corev1.LabelSelector { return l.sels }
