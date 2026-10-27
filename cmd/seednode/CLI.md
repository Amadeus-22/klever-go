
# Klever SeedNode CLI

The **Klever SeedNode** exposes the following Command Line Interface:

```
$ seednode --help

NAME:
   SeedNode CLI App - This is the entry point for starting a new seed node - the app will help bootnodes connect to the network
USAGE:
   seednode [global options]
   
AUTHOR:
   The Klever Team <contact@klever.org>
   
GLOBAL OPTIONS:
   --port [p2p port]              The [p2p port] number on which the application will start. Can use single values such as `0, 10230, 15670` or range of ports such as `5000-10000` (default: "10000")
   --p2p-key-pem-file filepath    The filepath for the PEM file which contains the p2p identity key. If the file does not exist, a new random key is generated and saved there. Saving needs a filesystem with hard-link support; otherwise create the file with `keygenerator --key-type p2p` and copy it here before startup. (default: "./config/p2pKey.pem")
   --log-level level(s)           This flag specifies the logger level(s). It can contain multiple comma-separated value. For example, if set to *:INFO the logs for all packages will have the INFO level. However, if set to *:INFO,api:DEBUG the logs for all packages will have the INFO level, excepting the api package which will receive a DEBUG log level. (default: "*:INFO")
   --log-save                     Boolean option for enabling log saving. If set, it will automatically save all the logs into a file.
   --config [path]                The [path] for the main configuration file. This YAML file contains the main configurations such as the marshalizer type (default: "./config/seednode/config.yaml")
   --rest-api-interface address   The interface address and port to which the REST API will attempt to bind. To bind to all available interfaces, set this flag to :8080. If set to "off" then the API won't be available (default: "localhost:8080")
   --help, -h                     show help
   --version, -v                  print the version
   

```

