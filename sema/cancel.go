package sema

import "context"

type cancellation struct {
	ctx         context.Context
	cancellable bool
	steps       uint32
	err         error
}

func (c *cancellation) poll() bool {
	if !c.cancellable {
		return false
	}
	c.steps++
	if c.steps%256 != 0 {
		return false
	}
	if err := c.ctx.Err(); err != nil {
		c.err = err
		return true
	}
	return false
}
