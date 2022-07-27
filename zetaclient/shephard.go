package zetaclient

import (
	"encoding/base64"
	"encoding/hex"
	ethcommon "github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/rs/zerolog/log"
	"github.com/zeta-chain/zetacore/common"
	"github.com/zeta-chain/zetacore/x/zetacore/types"
	"github.com/zeta-chain/zetacore/zetaclient/config"
	"math/big"
	"math/rand"
	"strings"
	"sync/atomic"
	"time"
)

func (co *CoreObserver) ShepherdManager() {
	numShepherds := 0
	for {
		select {
		case send := <-co.sendNew:
			if _, ok := co.shepherds[send.Index]; !ok {
				log.Info().Msgf("shepherd manager: new send %s", send.Index)
				co.shepherds[send.Index] = true
				log.Info().Msg("waiting on a signer slot...")
				<-co.signerSlots
				log.Info().Msg("got a signer slot! spawn shepherd")
				go co.shepherdSend(send)
				numShepherds++
				log.Info().Msgf("new shepherd: %d shepherds in total", numShepherds)
			}
		case send := <-co.sendDone:
			delete(co.shepherds, send.Index)
			numShepherds--
			log.Info().Msgf("remove shepherd: %d shepherds left", numShepherds)
		}
	}
}

// Once this function receives a Send, it will make sure that the send is processed and confirmed
// on external chains and ZetaCore.
// FIXME: make sure that ZetaCore is updated when the Send cannot be processed.
func (co *CoreObserver) shepherdSend(send *types.Send) {
	startTime := time.Now()
	confirmDone := make(chan bool, 1)
	coreSendDone := make(chan bool, 1)
	var numQueries int32 = 0
	var keysignCount int32 = 0

	defer func() {
		elapsedTime := time.Since(startTime)
		kc := atomic.LoadInt32(&keysignCount)
		nq := atomic.LoadInt32(&numQueries)
		if kc > 0 {
			log.Info().Msgf("shepherd stopped: numQueries %d; elapsed time %s; keysignCount %d", nq, elapsedTime, kc)
			co.fileLogger.Info().Msgf("shepherd stopped: numQueries %d; elapsed time %s; keysignCount %d", nq, elapsedTime, kc)
		}
		co.signerSlots <- true
		co.sendDone <- send
		confirmDone <- true
		coreSendDone <- true
	}()

	myid := co.bridge.keys.GetSignerInfo().GetAddress().String()
	amount, ok := new(big.Int).SetString(send.ZetaMint, 0)
	if !ok {
		log.Error().Msg("error converting MBurnt to big.Int")
		return
	}

	var to ethcommon.Address
	var err error
	var toChain common.Chain
	if send.Status == types.SendStatus_PendingRevert {
		to = ethcommon.HexToAddress(send.Sender)
		toChain, err = common.ParseChain(send.SenderChain)
		log.Info().Msgf("Abort: reverting inbound")
	} else if send.Status == types.SendStatus_PendingOutbound {
		to = ethcommon.HexToAddress(send.Receiver)
		toChain, err = common.ParseChain(send.ReceiverChain)
	}
	if err != nil {
		log.Error().Err(err).Msg("ParseChain fail; skip")
		return
	}

	// Early return if the send is already processed
	included, confirmed, _ := co.clientMap[toChain].IsSendOutTxProcessed(send.Index, int64(send.Nonce))
	if included || confirmed {
		log.Info().Msgf("sendHash %s already processed; exit signer", send.Index)
		return
	}

	signer := co.signerMap[toChain]
	message, err := base64.StdEncoding.DecodeString(send.Message)
	if err != nil {
		log.Err(err).Msgf("decode send.Message %s error", send.Message)
	}

	gasLimit := send.GasLimit
	if gasLimit < 50_000 {
		gasLimit = 50_000
	}

	log.Info().Msgf("chain %s minting %d to %s, nonce %d, finalized %d", toChain, amount, to.Hex(), send.Nonce, send.FinalizedMetaHeight)
	sendHash, err := hex.DecodeString(send.Index[2:]) // remove the leading 0x
	if err != nil || len(sendHash) != 32 {
		log.Err(err).Msgf("decode sendHash %s error", send.Index)
		return
	}
	var sendhash [32]byte
	copy(sendhash[:32], sendHash[:32])
	gasprice, ok := new(big.Int).SetString(send.GasPrice, 10)
	if !ok {
		log.Err(err).Msgf("cannot convert gas price  %s ", send.GasPrice)
		return
	}
	var tx *ethtypes.Transaction

	signloopDone := make(chan bool, 1)
	go func() {
		for {
			select {
			case <-confirmDone:
				return
			default:
				included, confirmed, err := co.clientMap[toChain].IsSendOutTxProcessed(send.Index, int64(send.Nonce))
				if err != nil {
					atomic.AddInt32(&numQueries, 1)
				}
				if included || confirmed {
					log.Info().Msgf("sendHash %s included; kill this shepherd", send.Index)
					signloopDone <- true
					return
				}
				time.Sleep(12 * time.Second)
			}
		}
	}()

	// watch ZetaCore /zeta-chain/send/<sendHash> endpoint; send coreSendDone when the state of the send is updated;
	// e.g. pendingOutbound->outboundMined; or pendingOutbound->pendingRevert
	go func() {
		for {
			select {
			case <-coreSendDone:
				return
			default:
				newSend, err := co.bridge.GetSendByHash(send.Index)
				if err != nil || send == nil {
					log.Info().Msgf("sendHash %s cannot be found in ZetaCore; kill the shepherd", send.Index)
					signloopDone <- true
				}
				if newSend.Status != send.Status {
					log.Info().Msgf("sendHash %s status changed to %s from %s; kill the shepherd", send.Index, newSend.Status, send.Status)
					signloopDone <- true
				}
				time.Sleep(12 * time.Second)
			}
		}
	}()

	// The following keysign loop tries to sign outbound tx until the following conditions are met:
	// 1. zetacore /zeta-chain/send/<sendHash> endpoint returns a changed status
	// 2. outTx is confirmed to be successfully or failed
	signTicker := time.NewTicker(time.Second)
	signInterval := 128 * time.Second // minimum gap between two keysigns
	lastSignTime := time.Unix(1, 0)
SIGNLOOP:
	for range signTicker.C {
		select {
		case <-signloopDone:
			log.Info().Msg("breaking SignOutBoundTx loop: outbound already processed")
			break SIGNLOOP
		default:
			minNonce := atomic.LoadInt64(&co.clientMap[toChain].MinNonce)
			maxNonce := atomic.LoadInt64(&co.clientMap[toChain].MaxNonce)
			if minNonce == int64(send.Nonce) && maxNonce > int64(send.Nonce)+5 {
				log.Warn().Msgf("this signer is likely blocking subsequent txs! nonce %d", send.Nonce)
				signInterval = 32 * time.Second
			}
			tnow := time.Now()
			if tnow.Before(lastSignTime.Add(signInterval)) {
				continue
			}
			if tnow.Unix()%16 == int64(sendhash[0])%16 { // weakly sync the TSS signers
				included, confirmed, _ := co.clientMap[toChain].IsSendOutTxProcessed(send.Index, int64(send.Nonce))
				if included || confirmed {
					log.Info().Msgf("sendHash %s already confirmed; skip it", send.Index)
					break SIGNLOOP
				}
				srcChainID := config.Chains[send.SenderChain].ChainID
				if send.Status == types.SendStatus_PendingRevert {
					log.Info().Msgf("SignRevertTx: %s => %s, nonce %d, sendHash %s", send.SenderChain, toChain, send.Nonce, send.Index)
					toChainID := config.Chains[send.ReceiverChain].ChainID
					tx, err = signer.SignRevertTx(ethcommon.HexToAddress(send.Sender), srcChainID, to.Bytes(), toChainID, amount, gasLimit, message, sendhash, send.Nonce, gasprice)
				} else if send.Status == types.SendStatus_PendingOutbound {
					log.Info().Msgf("SignOutboundTx: %s => %s, nonce %d, sendHash %s", send.SenderChain, toChain, send.Nonce, send.Index)
					tx, err = signer.SignOutboundTx(ethcommon.HexToAddress(send.Sender), srcChainID, to, amount, gasLimit, message, sendhash, send.Nonce, gasprice)
				}
				if err != nil {
					log.Warn().Err(err).Msgf("SignOutboundTx error: nonce %d chain %s", send.Nonce, send.ReceiverChain)
					continue
				}
				lastSignTime = time.Now()
				cnt, err := co.GetPromCounter(OUTBOUND_TX_SIGN_COUNT)
				if err != nil {
					log.Error().Err(err).Msgf("GetPromCounter error")
				} else {
					cnt.Inc()
				}

				// if tx is nil, maybe I'm not an active signer?
				if tx != nil {
					outTxHash := tx.Hash().Hex()
					log.Info().Msgf("on chain %s nonce %d, sendHash: %s, outTxHash %s signer %s", signer.chain, send.Nonce, send.Index[:6], outTxHash, myid)
					if myid == send.Signers[send.Broadcaster] || myid == send.Signers[int(send.Broadcaster+1)%len(send.Signers)] {
						backOff := 1000 * time.Millisecond
						for i := 0; i < 5; i++ { // retry loop: 1s, 2s, 4s, 8s, 16s
							log.Info().Msgf("broadcasting tx %s to chain %s: nonce %d, retry %d", outTxHash, toChain, send.Nonce, i)
							// #nosec G404 randomness is not a security issue here
							time.Sleep(time.Duration(rand.Intn(1500)) * time.Millisecond) //random delay to avoid sychronized broadcast
							err = signer.Broadcast(tx)
							// TODO: the following error handling is robust?
							if err == nil {
								log.Err(err).Msgf("Broadcast success: nonce %d chain %s outTxHash %s", send.Nonce, toChain, outTxHash)
								co.fileLogger.Err(err).Msgf("Broadcast success: nonce %d chain %s outTxHash %s", send.Nonce, toChain, outTxHash)
								break // break the retry loop
							} else if strings.Contains(err.Error(), "nonce too low") {
								log.Warn().Err(err).Msgf("nonce too low! this might be a unnecessary keysign. increase re-try interval and awaits outTx confirmation")
								co.fileLogger.Err(err).Msgf("Broadcast nonce too low: nonce %d chain %s outTxHash %s; increase re-try interval", send.Nonce, toChain, outTxHash)
								break
							} else if strings.Contains(err.Error(), "replacement transaction underpriced") {
								log.Warn().Err(err).Msgf("Broadcast replacement: nonce %d chain %s outTxHash %s", send.Nonce, toChain, outTxHash)
								co.fileLogger.Err(err).Msgf("Broadcast replacement: nonce %d chain %s outTxHash %s", send.Nonce, toChain, outTxHash)
								break
							} else if strings.Contains(err.Error(), "already known") { // this is error code from QuickNode
								log.Warn().Err(err).Msgf("Broadcast duplicates: nonce %d chain %s outTxHash %s", send.Nonce, toChain, outTxHash)
								co.fileLogger.Err(err).Msgf("Broadcast duplicates: nonce %d chain %s outTxHash %s", send.Nonce, toChain, outTxHash)
								break
							} else { // most likely an RPC error, such as timeout or being rate limited. Exp backoff retry
								log.Error().Err(err).Msgf("Broadcast error: nonce %d chain %s outTxHash %s; retring...", send.Nonce, toChain, outTxHash)
								co.fileLogger.Err(err).Msgf("Broadcast error: nonce %d chain %s outTxHash %s; retrying...", send.Nonce, toChain, outTxHash)
								time.Sleep(backOff)
							}
							backOff *= 2
						}

					}
					// if outbound tx fails, kill this shepherd, a new one will be later spawned.
					co.clientMap[toChain].AddTxHashToWatchList(outTxHash, int64(send.Nonce), send.Index)
					co.fileLogger.Info().Msgf("Keysign: %s => %s, nonce %d, outTxHash %s; keysignCount %d", send.SenderChain, toChain, send.Nonce, outTxHash, keysignCount)
					atomic.AddInt32(&keysignCount, 1)
					signInterval *= 2 // exponential backoff
				}
			}
		}
	}
}