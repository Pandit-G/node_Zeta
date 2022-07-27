package zetaclient

import (
	"errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/zeta-chain/zetacore/zetaclient/metrics"
)

func (ob *ChainObserver) GetPromCounter(name string) (prometheus.Counter, error) {
	if cnt, found := metrics.Counters[ob.chain.String()+"_"+name]; found {
		return cnt, nil
	} else {
		return nil, errors.New("counter not found")
	}
}

func (ob *ChainObserver) RegisterPromCounter(name string, help string) error {
	cntName := ob.chain.String() + "_" + name
	return ob.metrics.RegisterCounter(cntName, help)
}