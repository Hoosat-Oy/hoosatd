package rpc

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/daglabs/btcd/blockdag"
	"github.com/daglabs/btcd/btcjson"
	"github.com/daglabs/btcd/dagconfig"
	"github.com/daglabs/btcd/txscript"
	"github.com/daglabs/btcd/util"
	"github.com/daglabs/btcd/util/daghash"
	"github.com/daglabs/btcd/wire"
	"math/big"
	"strconv"
)

var (
	// ErrRPCUnimplemented is an error returned to RPC clients when the
	// provided command is recognized, but not implemented.
	ErrRPCUnimplemented = &btcjson.RPCError{
		Code:    btcjson.ErrRPCUnimplemented,
		Message: "Command unimplemented",
	}
)

// internalRPCError is a convenience function to convert an internal error to
// an RPC error with the appropriate code set.  It also logs the error to the
// RPC server subsystem since internal errors really should not occur.  The
// context parameter is only used in the log message and may be empty if it's
// not needed.
func internalRPCError(errStr, context string) *btcjson.RPCError {
	logStr := errStr
	if context != "" {
		logStr = context + ": " + errStr
	}
	log.Error(logStr)
	return btcjson.NewRPCError(btcjson.ErrRPCInternal.Code, errStr)
}

// rpcDecodeHexError is a convenience function for returning a nicely formatted
// RPC error which indicates the provided hex string failed to decode.
func rpcDecodeHexError(gotHex string) *btcjson.RPCError {
	return btcjson.NewRPCError(btcjson.ErrRPCDecodeHexString,
		fmt.Sprintf("Argument must be hexadecimal string (not %q)",
			gotHex))
}

// rpcNoTxInfoError is a convenience function for returning a nicely formatted
// RPC error which indicates there is no information available for the provided
// transaction hash.
func rpcNoTxInfoError(txID *daghash.TxID) *btcjson.RPCError {
	return btcjson.NewRPCError(btcjson.ErrRPCNoTxInfo,
		fmt.Sprintf("No information available about transaction %s",
			txID))
}

// messageToHex serializes a message to the wire protocol encoding using the
// latest protocol version and returns a hex-encoded string of the result.
func messageToHex(msg wire.Message) (string, error) {
	var buf bytes.Buffer
	if err := msg.BtcEncode(&buf, maxProtocolVersion); err != nil {
		context := fmt.Sprintf("Failed to encode msg of type %T", msg)
		return "", internalRPCError(err.Error(), context)
	}

	return hex.EncodeToString(buf.Bytes()), nil
}

// createVinList returns a slice of JSON objects for the inputs of the passed
// transaction.
func createVinList(mtx *wire.MsgTx) []btcjson.Vin {
	vinList := make([]btcjson.Vin, len(mtx.TxIn))
	for i, txIn := range mtx.TxIn {
		// The disassembled string will contain [error] inline
		// if the script doesn't fully parse, so ignore the
		// error here.
		disbuf, _ := txscript.DisasmString(txIn.SignatureScript)

		vinEntry := &vinList[i]
		vinEntry.TxID = txIn.PreviousOutpoint.TxID.String()
		vinEntry.Vout = txIn.PreviousOutpoint.Index
		vinEntry.Sequence = txIn.Sequence
		vinEntry.ScriptSig = &btcjson.ScriptSig{
			Asm: disbuf,
			Hex: hex.EncodeToString(txIn.SignatureScript),
		}
	}

	return vinList
}

// createVoutList returns a slice of JSON objects for the outputs of the passed
// transaction.
func createVoutList(mtx *wire.MsgTx, chainParams *dagconfig.Params, filterAddrMap map[string]struct{}) []btcjson.Vout {
	voutList := make([]btcjson.Vout, 0, len(mtx.TxOut))
	for i, v := range mtx.TxOut {
		// The disassembled string will contain [error] inline if the
		// script doesn't fully parse, so ignore the error here.
		disbuf, _ := txscript.DisasmString(v.ScriptPubKey)

		// Ignore the error here since an error means the script
		// couldn't parse and there is no additional information about
		// it anyways.
		scriptClass, addr, _ := txscript.ExtractScriptPubKeyAddress(
			v.ScriptPubKey, chainParams)

		// Encode the addresses while checking if the address passes the
		// filter when needed.
		passesFilter := len(filterAddrMap) == 0
		var encodedAddr *string
		if addr != nil {
			encodedAddr = btcjson.String(addr.EncodeAddress())

			// If the filter doesn't already pass, make it pass if
			// the address exists in the filter.
			if _, exists := filterAddrMap[*encodedAddr]; exists {
				passesFilter = true
			}
		}

		if !passesFilter {
			continue
		}

		var vout btcjson.Vout
		vout.N = uint32(i)
		vout.Value = v.Value
		vout.ScriptPubKey.Address = encodedAddr
		vout.ScriptPubKey.Asm = disbuf
		vout.ScriptPubKey.Hex = hex.EncodeToString(v.ScriptPubKey)
		vout.ScriptPubKey.Type = scriptClass.String()

		voutList = append(voutList, vout)
	}

	return voutList
}

// createTxRawResult converts the passed transaction and associated parameters
// to a raw transaction JSON object.
func createTxRawResult(dagParams *dagconfig.Params, mtx *wire.MsgTx,
	txID string, blkHeader *wire.BlockHeader, blkHash string,
	acceptingBlock *daghash.Hash, confirmations *uint64, isInMempool bool, txMass uint64) (*btcjson.TxRawResult, error) {

	mtxHex, err := messageToHex(mtx)
	if err != nil {
		return nil, err
	}

	var payloadHash string
	if mtx.PayloadHash != nil {
		payloadHash = mtx.PayloadHash.String()
	}

	txReply := &btcjson.TxRawResult{
		Hex:         mtxHex,
		TxID:        txID,
		Hash:        mtx.TxHash().String(),
		Size:        int32(mtx.SerializeSize()),
		Vin:         createVinList(mtx),
		Vout:        createVoutList(mtx, dagParams, nil),
		Version:     mtx.Version,
		LockTime:    mtx.LockTime,
		Subnetwork:  mtx.SubnetworkID.String(),
		Gas:         mtx.Gas,
		Mass:        txMass,
		PayloadHash: payloadHash,
		Payload:     hex.EncodeToString(mtx.Payload),
	}

	if blkHeader != nil {
		// This is not a typo, they are identical in bitcoind as well.
		txReply.Time = uint64(blkHeader.Timestamp.Unix())
		txReply.BlockTime = uint64(blkHeader.Timestamp.Unix())
		txReply.BlockHash = blkHash
	}

	txReply.Confirmations = confirmations
	txReply.IsInMempool = isInMempool
	if acceptingBlock != nil {
		txReply.AcceptedBy = btcjson.String(acceptingBlock.String())
	}

	return txReply, nil
}

// getDifficultyRatio returns the proof-of-work difficulty as a multiple of the
// minimum difficulty using the passed bits field from the header of a block.
func getDifficultyRatio(bits uint32, params *dagconfig.Params) float64 {
	// The minimum difficulty is the max possible proof-of-work limit bits
	// converted back to a number.  Note this is not the same as the proof of
	// work limit directly because the block difficulty is encoded in a block
	// with the compact form which loses precision.
	target := util.CompactToBig(bits)

	difficulty := new(big.Rat).SetFrac(params.PowMax, target)
	outString := difficulty.FloatString(8)
	diff, err := strconv.ParseFloat(outString, 64)
	if err != nil {
		log.Errorf("Cannot get difficulty: %s", err)
		return 0
	}
	return diff
}

func buildGetBlockVerboseResult(s *Server, block *util.Block, isVerboseTx bool) (*btcjson.GetBlockVerboseResult, error) {
	hash := block.Hash()
	params := s.cfg.DAGParams
	blockHeader := block.MsgBlock().Header

	// Get the block chain height.
	blockChainHeight, err := s.cfg.DAG.BlockChainHeightByHash(hash)
	if err != nil {
		context := "Failed to obtain block height"
		return nil, internalRPCError(err.Error(), context)
	}

	// Get the hashes for the next blocks unless there are none.
	var nextHashStrings []string
	if blockChainHeight < s.cfg.DAG.ChainHeight() { //TODO: (Ori) This is probably wrong. Done only for compilation
		childHashes, err := s.cfg.DAG.ChildHashesByHash(hash)
		if err != nil {
			context := "No next block"
			return nil, internalRPCError(err.Error(), context)
		}
		nextHashStrings = daghash.Strings(childHashes)
	}

	blockConfirmations, err := s.cfg.DAG.BlockConfirmationsByHash(hash)
	if err != nil {
		context := "Could not get block confirmations"
		return nil, internalRPCError(err.Error(), context)
	}

	blockBlueScore, err := s.cfg.DAG.BlueScoreByBlockHash(hash)
	if err != nil {
		context := "Could not get block blue score"
		return nil, internalRPCError(err.Error(), context)
	}

	pastUTXO, err := s.cfg.DAG.BlockPastUTXO(block.Hash())
	if err != nil {
		context := "Could not get block past utxo"
		return nil, internalRPCError(err.Error(), context)
	}
	blockMass, err := blockdag.CalcBlockMass(pastUTXO, block.Transactions())
	if err != nil {
		context := "Could not get block mass"
		return nil, internalRPCError(err.Error(), context)
	}

	isChainBlock := s.cfg.DAG.IsInSelectedParentChain(hash)

	result := &btcjson.GetBlockVerboseResult{
		Hash:                 hash.String(),
		Version:              blockHeader.Version,
		VersionHex:           fmt.Sprintf("%08x", blockHeader.Version),
		HashMerkleRoot:       blockHeader.HashMerkleRoot.String(),
		AcceptedIDMerkleRoot: blockHeader.AcceptedIDMerkleRoot.String(),
		UTXOCommitment:       blockHeader.UTXOCommitment.String(),
		ParentHashes:         daghash.Strings(blockHeader.ParentHashes),
		Nonce:                blockHeader.Nonce,
		Time:                 blockHeader.Timestamp.Unix(),
		Confirmations:        blockConfirmations,
		Height:               blockChainHeight,
		BlueScore:            blockBlueScore,
		Mass:                 blockMass,
		IsChainBlock:         isChainBlock,
		Size:                 int32(block.MsgBlock().SerializeSize()),
		Bits:                 strconv.FormatInt(int64(blockHeader.Bits), 16),
		Difficulty:           getDifficultyRatio(blockHeader.Bits, params),
		NextHashes:           nextHashStrings,
	}

	if isVerboseTx {
		transactions := block.Transactions()
		txNames := make([]string, len(transactions))
		for i, tx := range transactions {
			txNames[i] = tx.ID().String()
		}

		result.Tx = txNames
	} else {
		txns := block.Transactions()
		rawTxns := make([]btcjson.TxRawResult, len(txns))
		for i, tx := range txns {
			var acceptingBlock *daghash.Hash
			var confirmations *uint64
			if s.cfg.TxIndex != nil {
				acceptingBlock, err = s.cfg.TxIndex.BlockThatAcceptedTx(s.cfg.DAG, tx.ID())
				if err != nil {
					return nil, err
				}
				txConfirmations, err := txConfirmations(s, tx.ID())
				if err != nil {
					return nil, err
				}
				confirmations = &txConfirmations
			}
			txMass, err := blockdag.CalcTxMassFromUTXOSet(tx, pastUTXO)
			if err != nil {
				return nil, err
			}
			rawTxn, err := createTxRawResult(params, tx.MsgTx(), tx.ID().String(),
				&blockHeader, hash.String(), acceptingBlock, confirmations, false, txMass)
			if err != nil {
				return nil, err
			}
			rawTxns[i] = *rawTxn
		}
		result.RawTx = rawTxns
	}

	return result, nil
}

func collectChainBlocks(s *Server, hashes []*daghash.Hash) ([]btcjson.ChainBlock, error) {
	chainBlocks := make([]btcjson.ChainBlock, 0, len(hashes))
	for _, hash := range hashes {
		acceptanceData, err := s.cfg.AcceptanceIndex.TxsAcceptanceData(hash)
		if err != nil {
			return nil, &btcjson.RPCError{
				Code:    btcjson.ErrRPCInternal.Code,
				Message: fmt.Sprintf("could not retrieve acceptance data for block %s", hash),
			}
		}

		acceptedBlocks := make([]btcjson.AcceptedBlock, 0, len(acceptanceData))
		for blockHash, blockAcceptanceData := range acceptanceData {
			acceptedTxIds := make([]string, 0, len(blockAcceptanceData))
			for _, txAcceptanceData := range blockAcceptanceData {
				if txAcceptanceData.IsAccepted {
					acceptedTxIds = append(acceptedTxIds, txAcceptanceData.Tx.ID().String())
				}
			}
			acceptedBlock := btcjson.AcceptedBlock{
				Hash:          blockHash.String(),
				AcceptedTxIDs: acceptedTxIds,
			}
			acceptedBlocks = append(acceptedBlocks, acceptedBlock)
		}

		chainBlock := btcjson.ChainBlock{
			Hash:           hash.String(),
			AcceptedBlocks: acceptedBlocks,
		}
		chainBlocks = append(chainBlocks, chainBlock)
	}
	return chainBlocks, nil
}

func hashesToGetBlockVerboseResults(s *Server, hashes []*daghash.Hash) ([]btcjson.GetBlockVerboseResult, error) {
	getBlockVerboseResults := make([]btcjson.GetBlockVerboseResult, 0, len(hashes))
	for _, blockHash := range hashes {
		block, err := s.cfg.DAG.BlockByHash(blockHash)
		if err != nil {
			return nil, &btcjson.RPCError{
				Code:    btcjson.ErrRPCInternal.Code,
				Message: fmt.Sprintf("could not retrieve block %s.", blockHash),
			}
		}
		getBlockVerboseResult, err := buildGetBlockVerboseResult(s, block, false)
		if err != nil {
			return nil, &btcjson.RPCError{
				Code:    btcjson.ErrRPCInternal.Code,
				Message: fmt.Sprintf("could not build getBlockVerboseResult for block %s.", blockHash),
			}
		}
		getBlockVerboseResults = append(getBlockVerboseResults, *getBlockVerboseResult)
	}
	return getBlockVerboseResults, nil
}

// txConfirmations returns the confirmations number for the given transaction
// The confirmations number is defined as follows:
// If the transaction is in the mempool/in a red block/is a double spend -> 0
// Otherwise -> The confirmations number of the accepting block
func txConfirmations(s *Server, txID *daghash.TxID) (uint64, error) {
	if s.cfg.TxIndex == nil {
		return 0, errors.New("transaction index must be enabled (--txindex)")
	}

	acceptingBlock, err := s.cfg.TxIndex.BlockThatAcceptedTx(s.cfg.DAG, txID)
	if err != nil {
		return 0, fmt.Errorf("could not get block that accepted tx %s: %s", txID, err)
	}
	if acceptingBlock == nil {
		return 0, nil
	}

	confirmations, err := s.cfg.DAG.BlockConfirmationsByHash(acceptingBlock)
	if err != nil {
		return 0, fmt.Errorf("could not get confirmations for block that accepted tx %s: %s", txID, err)
	}

	return confirmations, nil
}