# LocalNet - Local Development Environment

This create a local multi-chain development environment

It Supports:

- Polygon
- Binance Smart Chain (BSC)
- Ethereum
- ZetaChain (2 zetacore + 2 zetaclient containers)

## PreReqs

You must have the following installed

- git
- docker
- yarn

## How to use LocalNet

Update the `.env` file with the path to your local Zeta-Contracts repo

If `USE_GANACHE=true` ganache will be used to replicat the external networks. This results in a much faster development environments but may not be identical to the real responses you'd receive from real external chain nodes.
If `USE_GANACHE=false` a seperate set of nodes will be deployed for each external chain. This includes geth, bsc-geth, and bor. 

The first time you run localnet you must install the dependencies, build the docker images, then start the different network nodes.  
```
yarn install
yarn build
yarn start
```

If you want to rebuild images for a specific chain (like Zetachain) cd into `localnet/zetachain` and run `build.sh`. After it has been rebuilt run `start.sh` to deploy the latest image. If you want to clear out the saved blockchain data and start fresh, cd into the network you want to update and run `./stop.sh && ./reset`.

If you want to reset all chains you can use `yarn reset`

## Zetachain 

These scripts and docker files build and deploy zetacored and zetaclientd in seperate containers with a shared file system. Anytime we refer to a 'node' it refers to a zetacored container that's been paired with a zetaclientd container. For example, zetacore0 + zetaclient0 = node0

Use the `yarn start` command before you launch the zetachain network. After all the other chains have been deployed you can rebuild the Zetachain containers with `yarn build-zeta` and deploy them with `yarn start-zeta`. 

If you want to rebuild from the latest source code and start over zetachain from scratch you can run this combination `yarn build-zeta && yarn reset-zeta && yarn start-zeta`

TSS keys are generated dynamically on startup and after 30-60 seconds will be detected by the  `./zetachain/start.sh` script and automatically whitelisted in the MPI contracts. 
### Directory/File Structure

#### zetachain/.env
The `.env` file is automatically overwritten by the contents of `env_vars` when you run the build script. If you want to make a permentant change to `.env` you must update `env_vars` and then run the build.sh script. `.env` is used to pass arguments to docker compose include the MPI Contract Addresses for the connected chains. 

#### zetachain/storage 
This directory is mapped to .zetacore, .zetaclient, .tssnew in all the containers. Each core/client container combo share one directory identified by a node number. 

You can safely delete this data anytime using `yarn reset-zeta` to clear out all the files reset the blockchain. 

#### zetachain/config

Semi permanent storage of the zeta config files, includes the genesis files. You can generate this files once and reuse them over and over again even if your reset the blockchain by removing the `./storage` directory. 

## Port Mapping

The HTTP/JSON RPC node for each chain is mapped to a different port on your local host.

- eth: localhost:8100
- bsc: localhost:8120
- polygon: localhost:8140
- zeta: localhost:1317

## RPC Commands

Some bash commands for interacting the chains have been added to the `rpc_commands` file. To temporarily add them to your terminal run `source rpc_commands`. Check out the file for more details. 

I used these for troubleshooting when setting up this nodes but I don't think anyone will need them for normal operations.

## Problems and Additional Notes

### BSC gets out of sync or may not be working
Sometimes the BSC containers get out of sync and you can no longer deploy contracts. If you think this has happened check http://localhost:3010/ and make sure the block counts are the same. 

If this happens stop all the BSC containers and then start them again. That usually fixes it but if it doesn't you'll need to stop the containers, delete the `bsc/storage` folder, then run `build.sh` again followed by `start.sh`


## ToDo

- Remove Contract code from this repo and pull from `zeta-contracts` as needed (git submodule add git@github.com:zeta-chain/zeta-contracts.git localnet/zeta-contracts )
- Optimization! There's a lot of room for optimzation in the build process and the docker compose configurations. 
- Better solution for .env than copying it
- Move contract values to JSON or another better storage solution that a bunch of text files
    - Use Lucas's solution in the other repo for this
- Test which images can work as ARM and then removing the platform flags. I ran into issues with some of them earlier on so to be save I started forcing them all to run at amd64
- Long term - Rewrite contract deployments and test as a more full featured and flexible node script
- Improve the testing scripts so the same one can be used for all networks and just pass in different network values. 
- Use 