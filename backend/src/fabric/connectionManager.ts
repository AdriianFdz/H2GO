import {
  connect,
  Gateway,
  Identity,
  Signer,
  signers,
} from '@hyperledger/fabric-gateway';
import * as grpc from '@grpc/grpc-js';
import { promises as fs } from 'fs';
import * as yaml from 'js-yaml';
import * as path from 'path';
import * as crypto from 'crypto';
import { Injectable } from '@nestjs/common';
import { IAuthenticatedUser } from '../auth/interfaces/authenticatedUser';

interface UserCredentials {
  cert: { pem: string };
  key: { pem: string };
}

@Injectable()
export class ConnectionManager {
  constructor() {}

  private async loadCredentialsFromYaml(
    yamlPath: string,
  ): Promise<UserCredentials> {
    const fileContent = await fs.readFile(yamlPath, 'utf8');
    return yaml.load(fileContent) as UserCredentials;
  }

  private async loadTLSCACert(mspId: string): Promise<Buffer> {
    // Leer el network.yaml que contiene los certificados TLS CA de los peers
    const networkYamlPath = path.join(
      process.env.FABRIC_RESOURCES_PATH!,
      process.env.NODE_ENV === 'test'
        ? 'test-network.yaml'
        : 'network.yaml',
    );
    const fileContent = await fs.readFile(networkYamlPath, 'utf8');
    const networkConfig: any = yaml.load(fileContent);

    // Construir el nombre del peer en el formato del network.yaml
    const peerName = `${mspId.replace('msp', '')}-peer0.default`;
    const peerConfig = networkConfig.peers?.[peerName];

    if (!peerConfig || !peerConfig.tlsCACerts?.pem) {
      throw new Error(
        `TLS CA not found for peer ${peerName}`,
      );
    }

    return Buffer.from(peerConfig.tlsCACerts.pem);
  }

  async connectGateway(
    user: IAuthenticatedUser,
  ): Promise<{ gateway: Gateway; client: grpc.Client }> {
    const mspId = user.organization.mspId.toLowerCase();

    // Rutas a los archivos de credenciales
    const credentialsPath = path.join(
      process.env.FABRIC_RESOURCES_PATH!,
      `peer-${mspId.replace('msp', '')}.yaml`,
    );

    const credentials = await this.loadCredentialsFromYaml(credentialsPath);
    const tlsRootCert = await this.loadTLSCACert(mspId);

    // Configurar credenciales gRPC con TLS
    const grpcCredentials = grpc.credentials.createSsl(
      tlsRootCert,
      null,
      null,
      {
        checkServerIdentity: () => undefined, // Deshabilitar verificación de hostname para desarrollo
      },
    );

    // Crear cliente gRPC
    const client = new grpc.Client(
      user.organization.peerEndpoint,
      grpcCredentials,
      {
        'grpc.ssl_target_name_override':
          user.organization.peerEndpoint.split(':')[0],
        'grpc.default_authority': user.organization.peerEndpoint.split(':')[0],
      },
    );

    // Crear identidad y signer para el gateway
    const identity: Identity = {
      mspId: user.organization.mspId,
      credentials: Buffer.from(credentials.cert.pem),
    };

    const privateKey = crypto.createPrivateKey(credentials.key.pem);
    const signer: Signer = signers.newPrivateKeySigner(privateKey);

    // Conectar al gateway
    const gateway = connect({
      client,
      identity,
      signer,
      evaluateOptions: () => ({ deadline: Date.now() + 15000 }),
      endorseOptions: () => ({ deadline: Date.now() + 15000 }),
      submitOptions: () => ({ deadline: Date.now() + 15000 }),
      commitStatusOptions: () => ({ deadline: Date.now() + 60000 }),
    });

    return { gateway, client };
  }

  disconnectGateway(gateway: Gateway, client: grpc.Client): void {
    gateway.close();
    client.close();
  }

  queryTransaction(
    gateway: Gateway,
    functionName: string,
    ...args: string[]
  ): Promise<Uint8Array> {
    const network = gateway.getNetwork(process.env.FABRIC_CHANNEL_NAME!);
    const contract = network.getContract(process.env.FABRIC_CHAINCODE_NAME!);
    const result = contract.evaluateTransaction(functionName, ...args);
    return result;
  }

  executeTransaction(
    gateway: Gateway,
    functionName: string,
    ...args: string[]
  ): Promise<Uint8Array> {
    const network = gateway.getNetwork(process.env.FABRIC_CHANNEL_NAME!);
    const contract = network.getContract(process.env.FABRIC_CHAINCODE_NAME!);
    const result = contract.submitTransaction(functionName, ...args);
    return result;
  }
}
