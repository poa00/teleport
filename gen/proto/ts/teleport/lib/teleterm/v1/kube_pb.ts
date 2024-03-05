/* eslint-disable */
// @generated by protobuf-ts 2.9.3 with parameter long_type_number,eslint_disable,add_pb_suffix,server_grpc1,ts_nocheck
// @generated from protobuf file "teleport/lib/teleterm/v1/kube.proto" (package "teleport.lib.teleterm.v1", syntax proto3)
// tslint:disable
// @ts-nocheck
//
//
// Teleport
// Copyright (C) 2023  Gravitational, Inc.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>.
//
import type { BinaryWriteOptions } from "@protobuf-ts/runtime";
import type { IBinaryWriter } from "@protobuf-ts/runtime";
import { WireType } from "@protobuf-ts/runtime";
import type { BinaryReadOptions } from "@protobuf-ts/runtime";
import type { IBinaryReader } from "@protobuf-ts/runtime";
import { UnknownFieldHandler } from "@protobuf-ts/runtime";
import type { PartialMessage } from "@protobuf-ts/runtime";
import { reflectionMergePartial } from "@protobuf-ts/runtime";
import { MessageType } from "@protobuf-ts/runtime";
import { Label } from "./label_pb";
/**
 * Kube describes connected Kubernetes cluster
 *
 * @generated from protobuf message teleport.lib.teleterm.v1.Kube
 */
export interface Kube {
    /**
     * uri is the kube resource URI
     *
     * @generated from protobuf field: string uri = 1;
     */
    uri: string;
    /**
     * name is the kube name
     *
     * @generated from protobuf field: string name = 2;
     */
    name: string;
    /**
     * labels is the kube labels
     *
     * @generated from protobuf field: repeated teleport.lib.teleterm.v1.Label labels = 3;
     */
    labels: Label[];
}
// @generated message type with reflection information, may provide speed optimized methods
class Kube$Type extends MessageType<Kube> {
    constructor() {
        super("teleport.lib.teleterm.v1.Kube", [
            { no: 1, name: "uri", kind: "scalar", T: 9 /*ScalarType.STRING*/ },
            { no: 2, name: "name", kind: "scalar", T: 9 /*ScalarType.STRING*/ },
            { no: 3, name: "labels", kind: "message", repeat: 1 /*RepeatType.PACKED*/, T: () => Label }
        ]);
    }
    create(value?: PartialMessage<Kube>): Kube {
        const message = globalThis.Object.create((this.messagePrototype!));
        message.uri = "";
        message.name = "";
        message.labels = [];
        if (value !== undefined)
            reflectionMergePartial<Kube>(this, message, value);
        return message;
    }
    internalBinaryRead(reader: IBinaryReader, length: number, options: BinaryReadOptions, target?: Kube): Kube {
        let message = target ?? this.create(), end = reader.pos + length;
        while (reader.pos < end) {
            let [fieldNo, wireType] = reader.tag();
            switch (fieldNo) {
                case /* string uri */ 1:
                    message.uri = reader.string();
                    break;
                case /* string name */ 2:
                    message.name = reader.string();
                    break;
                case /* repeated teleport.lib.teleterm.v1.Label labels */ 3:
                    message.labels.push(Label.internalBinaryRead(reader, reader.uint32(), options));
                    break;
                default:
                    let u = options.readUnknownField;
                    if (u === "throw")
                        throw new globalThis.Error(`Unknown field ${fieldNo} (wire type ${wireType}) for ${this.typeName}`);
                    let d = reader.skip(wireType);
                    if (u !== false)
                        (u === true ? UnknownFieldHandler.onRead : u)(this.typeName, message, fieldNo, wireType, d);
            }
        }
        return message;
    }
    internalBinaryWrite(message: Kube, writer: IBinaryWriter, options: BinaryWriteOptions): IBinaryWriter {
        /* string uri = 1; */
        if (message.uri !== "")
            writer.tag(1, WireType.LengthDelimited).string(message.uri);
        /* string name = 2; */
        if (message.name !== "")
            writer.tag(2, WireType.LengthDelimited).string(message.name);
        /* repeated teleport.lib.teleterm.v1.Label labels = 3; */
        for (let i = 0; i < message.labels.length; i++)
            Label.internalBinaryWrite(message.labels[i], writer.tag(3, WireType.LengthDelimited).fork(), options).join();
        let u = options.writeUnknownFields;
        if (u !== false)
            (u == true ? UnknownFieldHandler.onWrite : u)(this.typeName, message, writer);
        return writer;
    }
}
/**
 * @generated MessageType for protobuf message teleport.lib.teleterm.v1.Kube
 */
export const Kube = new Kube$Type();
